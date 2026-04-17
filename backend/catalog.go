package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// ── interfaces ────────────────────────────────────────────────────────────────

// partCatalogReader is the minimal interface consumed by the analysis pipeline.
// Implementations are org-scoped (either natively or via orgScopedCatalog).
type partCatalogReader interface {
	findByMPN(mpn string) (*CatalogPart, bool, error)
	findByType(partType string) ([]*CatalogPart, error)
	incrementUsage(id string)
}

// partCatalogRepository is the full interface used by HTTP handlers and
// the mapping-save path. All operations are explicitly org-scoped.
type partCatalogRepository interface {
	upsert(p *CatalogPart, orgID string) error
	findByMPN(mpn, orgID string) (*CatalogPart, bool, error)
	findByType(partType, orgID string) ([]*CatalogPart, error)
	incrementUsage(id string) error
}

// orgScopedCatalog binds a partCatalogRepository to a fixed orgID,
// satisfying the partCatalogReader interface used by the analysis pipeline.
type orgScopedCatalog struct {
	repo  partCatalogRepository
	orgID string
}

func (c *orgScopedCatalog) findByMPN(mpn string) (*CatalogPart, bool, error) {
	return c.repo.findByMPN(mpn, c.orgID)
}

func (c *orgScopedCatalog) findByType(partType string) ([]*CatalogPart, error) {
	return c.repo.findByType(partType, c.orgID)
}

func (c *orgScopedCatalog) incrementUsage(id string) {
	if err := c.repo.incrementUsage(id); err != nil {
		log.Printf("catalog.incrementUsage %s: %v", id, err)
	}
}

// ── pgPartCatalogRepository ───────────────────────────────────────────────────

type pgPartCatalogRepository struct {
	db *sql.DB
}

func (r *pgPartCatalogRepository) upsert(p *CatalogPart, orgID string) error {
	if p.InternalPartNumber == "" {
		return fmt.Errorf("internalPartNumber is required")
	}
	fpJSON, err := json.Marshal(p.Fingerprint)
	if err != nil {
		return fmt.Errorf("marshal fingerprint: %w", err)
	}
	return r.db.QueryRow(`
		INSERT INTO part_catalog
			(organization_id, internal_part_number, manufacturer_part_number,
			 description, fingerprint)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, internal_part_number) DO UPDATE SET
			manufacturer_part_number = EXCLUDED.manufacturer_part_number,
			description              = EXCLUDED.description,
			fingerprint              = EXCLUDED.fingerprint,
			updated_at               = now()
		RETURNING id, created_at, updated_at`,
		orgID, p.InternalPartNumber, p.ManufacturerPartNumber, p.Description, string(fpJSON),
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *pgPartCatalogRepository) findByMPN(mpn, orgID string) (*CatalogPart, bool, error) {
	key := strings.ToUpper(strings.TrimSpace(mpn))
	if key == "" {
		return nil, false, nil
	}
	var p CatalogPart
	var fpJSON string
	var lastUsed sql.NullTime
	err := r.db.QueryRow(`
		SELECT id, organization_id, internal_part_number, manufacturer_part_number,
		       description, fingerprint, usage_count, last_used_at, created_at, updated_at
		FROM part_catalog
		WHERE organization_id = $1 AND UPPER(manufacturer_part_number) = $2`,
		orgID, key,
	).Scan(&p.ID, &p.OrganizationID, &p.InternalPartNumber, &p.ManufacturerPartNumber,
		&p.Description, &fpJSON, &p.UsageCount, &lastUsed, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("findByMPN: %w", err)
	}
	p.LastUsedAt = scanNullTime(&lastUsed)
	if err := json.Unmarshal([]byte(fpJSON), &p.Fingerprint); err != nil {
		return nil, false, fmt.Errorf("unmarshal fingerprint: %w", err)
	}
	return &p, true, nil
}

func (r *pgPartCatalogRepository) findByType(partType, orgID string) ([]*CatalogPart, error) {
	if partType == "" {
		return nil, nil
	}
	rows, err := r.db.Query(`
		SELECT id, organization_id, internal_part_number, manufacturer_part_number,
		       description, fingerprint, usage_count, last_used_at, created_at, updated_at
		FROM part_catalog
		WHERE organization_id = $1 AND fingerprint->>'type' = $2
		ORDER BY usage_count DESC
		LIMIT 200`,
		orgID, partType,
	)
	if err != nil {
		return nil, fmt.Errorf("findByType: %w", err)
	}
	defer rows.Close()

	var out []*CatalogPart
	for rows.Next() {
		var p CatalogPart
		var fpJSON string
		var lastUsed sql.NullTime
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.InternalPartNumber, &p.ManufacturerPartNumber,
			&p.Description, &fpJSON, &p.UsageCount, &lastUsed, &p.CreatedAt, &p.UpdatedAt); err != nil {
			log.Printf("catalog.findByType scan: %v", err)
			continue
		}
		p.LastUsedAt = scanNullTime(&lastUsed)
		if err := json.Unmarshal([]byte(fpJSON), &p.Fingerprint); err != nil {
			log.Printf("catalog.findByType unmarshal: %v", err)
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

func (r *pgPartCatalogRepository) incrementUsage(id string) error {
	_, err := r.db.Exec(`
		UPDATE part_catalog
		SET usage_count = usage_count + 1, last_used_at = now(), updated_at = now()
		WHERE id = $1`, id)
	return err
}

// ── scoring ───────────────────────────────────────────────────────────────────

// attribute defines how a single fingerprint field contributes to scoring.
type attribute struct {
	name    string
	catalog string
	row     string
	fatal   bool    // if true, a mismatch returns score 0 immediately
	weight  float64
}

// scoreFingerprint returns a 0.0–1.0 score comparing a catalog part's fingerprint
// to a new row's fingerprint, along with human-readable match reasons.
//
// Only attributes present on BOTH sides are scored. A missing attribute on
// either side contributes nothing to numerator or denominator.
//
// Type and diameter mismatches are fatal — they return 0 immediately.
func scoreFingerprint(catalog, row PartFingerprint) (float64, []string) {
	attrs := []attribute{
		{"type", catalog.Type, row.Type, true, 0.30},
		{"diameter", catalog.Diameter, row.Diameter, true, 0.30},
		{"standard", catalog.Standard, row.Standard, false, 0.25},
		{"color", catalog.Color, row.Color, false, 0.10},
		{"material", catalog.Material, row.Material, false, 0.05},
	}

	var totalWeight float64
	var matchWeight float64
	var reasons []string

	for _, a := range attrs {
		if a.catalog == "" || a.row == "" {
			continue
		}
		totalWeight += a.weight
		if a.catalog == a.row {
			matchWeight += a.weight
			reasons = append(reasons, a.name+": "+a.catalog)
		} else if a.fatal {
			return 0, nil
		}
	}

	if totalWeight == 0 {
		return 0, nil
	}
	return matchWeight / totalWeight, reasons
}

// ── suggestion pipeline ───────────────────────────────────────────────────────

const (
	// catalogAutoAcceptThreshold: apply IPN without showing the suggestion UI.
	catalogAutoAcceptThreshold = 0.90
	// catalogSuggestThreshold: show suggestion for user to accept/reject.
	catalogSuggestThreshold = 0.50
)

// suggestFromCatalog builds a fingerprint for row, queries the catalog, and
// returns the best-scoring candidate above the suggest threshold (or nil).
// Returns nil without error when catalog is nil.
func suggestFromCatalog(row *BOMRow, catalog partCatalogReader) (*PartSuggestion, error) {
	if catalog == nil {
		return nil, nil
	}

	// 1. Exact MPN match (highest confidence).
	if row.ManufacturerPartNumber != "" {
		p, ok, err := catalog.findByMPN(row.ManufacturerPartNumber)
		if err != nil {
			return nil, fmt.Errorf("catalog.findByMPN: %w", err)
		}
		if ok {
			go catalog.incrementUsage(p.ID)
			return &PartSuggestion{
				CatalogPartID:          p.ID,
				InternalPartNumber:     p.InternalPartNumber,
				ManufacturerPartNumber: p.ManufacturerPartNumber,
				Score:                  1.0,
				Source:                 "exact_mpn",
				MatchReasons:           []string{"exact manufacturer part number match"},
			}, nil
		}
	}

	// 2. Fingerprint match.
	fp := buildFingerprint(row.Description)
	if fp.Type == "" {
		// Without a type we can't narrow candidates — skip to avoid noise.
		return nil, nil
	}

	candidates, err := catalog.findByType(fp.Type)
	if err != nil {
		return nil, fmt.Errorf("catalog.findByType: %w", err)
	}

	var bestScore float64
	var bestPart *CatalogPart
	var bestReasons []string

	for _, c := range candidates {
		score, reasons := scoreFingerprint(c.Fingerprint, fp)
		if score > bestScore {
			bestScore = score
			bestPart = c
			bestReasons = reasons
		}
	}

	if bestScore < catalogSuggestThreshold {
		return nil, nil
	}

	go catalog.incrementUsage(bestPart.ID)

	return &PartSuggestion{
		CatalogPartID:          bestPart.ID,
		InternalPartNumber:     bestPart.InternalPartNumber,
		ManufacturerPartNumber: bestPart.ManufacturerPartNumber,
		Score:                  bestScore,
		Source:                 "fingerprint",
		MatchReasons:           bestReasons,
	}, nil
}

// upsertCatalogFromMapping builds a CatalogPart from a Mapping and upserts it.
// Called whenever a mapping is saved, so the catalog stays in sync with known
// parts. No-ops when InternalPartNumber is empty.
func upsertCatalogFromMapping(m *Mapping, repo partCatalogRepository, orgID string) {
	if m.InternalPartNumber == "" {
		return
	}
	p := &CatalogPart{
		InternalPartNumber:     m.InternalPartNumber,
		ManufacturerPartNumber: m.ManufacturerPartNumber,
		Description:            m.Description,
		Fingerprint:            buildFingerprint(m.Description),
	}
	if err := repo.upsert(p, orgID); err != nil {
		log.Printf("catalog.upsert from mapping %s: %v", m.CustomerPartNumber, err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// scanLastUsedAt handles the nullable last_used_at column.
func scanNullTime(ns *sql.NullTime) time.Time {
	if ns.Valid {
		return ns.Time
	}
	return time.Time{}
}
