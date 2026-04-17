package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
)

// ── interfaces ────────────────────────────────────────────────────────────────

// mappingReader is the minimal interface consumed by the analysis pipeline.
// Implementations are org-scoped (either natively or via orgScopedMappings).
type mappingReader interface {
	lookup(customerPartNumber string) (*Mapping, bool)
	touchLastUsed(customerPartNumber string)
}

// mappingRepository is the full CRUD interface used by HTTP handlers.
// All operations are explicitly scoped to an orgID.
type mappingRepository interface {
	save(m *Mapping, orgID string) error
	lookup(customerPartNumber, orgID string) (*Mapping, bool)
	all(orgID string) []*Mapping
	touchLastUsed(customerPartNumber, orgID string)
	// suggest returns up to limit mappings whose description or customer part
	// number contains any token from the query string (case-insensitive).
	suggest(query, orgID string, limit int) []*Mapping
}

// orgScopedMappings binds a mappingRepository to a fixed orgID, satisfying
// the mappingReader interface used by the analysis pipeline.
type orgScopedMappings struct {
	repo  mappingRepository
	orgID string
}

func (o *orgScopedMappings) lookup(cpn string) (*Mapping, bool) {
	return o.repo.lookup(cpn, o.orgID)
}

func (o *orgScopedMappings) touchLastUsed(cpn string) {
	o.repo.touchLastUsed(cpn, o.orgID)
}

// ── pgMappingRepository ───────────────────────────────────────────────────────

type pgMappingRepository struct {
	db *sql.DB
}

func (r *pgMappingRepository) save(m *Mapping, orgID string) error {
	key := normKey(m.CustomerPartNumber)
	if key == "" {
		return fmt.Errorf("customerPartNumber is required")
	}
	if m.Source == "" {
		m.Source = "manual"
	}
	if m.Confidence == 0 {
		m.Confidence = 1.0
	}
	return r.db.QueryRow(`
		INSERT INTO mappings
			(organization_id, customer_part_number, internal_part_number,
			 manufacturer_part_number, description, source, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (organization_id, customer_part_number) DO UPDATE SET
			internal_part_number     = EXCLUDED.internal_part_number,
			manufacturer_part_number = EXCLUDED.manufacturer_part_number,
			description              = EXCLUDED.description,
			source                   = EXCLUDED.source,
			confidence               = EXCLUDED.confidence,
			updated_at               = now()
		RETURNING id, created_at, updated_at, last_used_at`,
		orgID, key,
		m.InternalPartNumber, m.ManufacturerPartNumber, m.Description,
		m.Source, m.Confidence,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.LastUsedAt)
}

func (r *pgMappingRepository) lookup(cpn, orgID string) (*Mapping, bool) {
	var m Mapping
	err := r.db.QueryRow(`
		SELECT id, organization_id, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1 AND customer_part_number = $2`,
		orgID, normKey(cpn),
	).Scan(&m.ID, &m.OrganizationID, &m.CustomerPartNumber, &m.InternalPartNumber,
		&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
		&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		log.Printf("mapping lookup error: %v", err)
		return nil, false
	}
	return &m, true
}

func (r *pgMappingRepository) all(orgID string) []*Mapping {
	rows, err := r.db.Query(`
		SELECT id, organization_id, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1
		ORDER BY customer_part_number`,
		orgID,
	)
	if err != nil {
		log.Printf("mapping all error: %v", err)
		return nil
	}
	defer rows.Close()
	var result []*Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.CustomerPartNumber, &m.InternalPartNumber,
			&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
			&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			log.Printf("mapping scan error: %v", err)
			continue
		}
		result = append(result, &m)
	}
	return result
}

func (r *pgMappingRepository) suggest(query, orgID string, limit int) []*Mapping {
	if strings.TrimSpace(query) == "" {
		return []*Mapping{}
	}
	pattern := "%" + strings.ToLower(query) + "%"
	rows, err := r.db.Query(`
		SELECT id, organization_id, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1
		  AND (LOWER(description) LIKE $2 OR LOWER(customer_part_number) LIKE $2)
		ORDER BY last_used_at DESC
		LIMIT $3`,
		orgID, pattern, limit,
	)
	if err != nil {
		log.Printf("mapping suggest error: %v", err)
		return []*Mapping{}
	}
	defer rows.Close()
	var result []*Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.CustomerPartNumber, &m.InternalPartNumber,
			&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
			&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			log.Printf("mapping suggest scan error: %v", err)
			continue
		}
		result = append(result, &m)
	}
	return result
}

func (r *pgMappingRepository) touchLastUsed(cpn, orgID string) {
	_, err := r.db.Exec(`
		UPDATE mappings SET last_used_at = now()
		WHERE organization_id = $1 AND customer_part_number = $2`,
		orgID, normKey(cpn),
	)
	if err != nil {
		log.Printf("touchLastUsed error: %v", err)
	}
}

func normKey(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
