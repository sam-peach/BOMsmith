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
//
// touchLastUsed takes clientLabel so it can scope to the exact bucket that
// fired the lookup. Without this, two clients sharing a CPN have their
// last_used_at bumped together — wrecking the ranking the search UI relies on.
type mappingReader interface {
	lookup(customerPartNumber string) (*Mapping, bool)
	touchLastUsed(customerPartNumber, clientLabel string)
}

// mappingRepository is the full CRUD interface used by HTTP handlers.
// All operations are explicitly scoped to an orgID. Where applicable they
// also accept a clientLabel — empty string means the generic / pooled bucket.
type mappingRepository interface {
	save(m *Mapping, orgID string) error
	lookup(customerPartNumber, orgID string) (*Mapping, bool)
	// lookupClient queries a specific (org, client) bucket without falling
	// back. Used by import endpoints and tests that want exact-bucket reads.
	lookupClient(customerPartNumber, orgID, clientLabel string) (*Mapping, bool)
	all(orgID string) []*Mapping
	clients(orgID string) []ClientMappingSummary
	// touchLastUsed updates last_used_at on exactly one row: the (org, client,
	// cpn) tuple that fired the lookup. Cross-client CPN collisions are why
	// we can't filter by (org, cpn) alone.
	touchLastUsed(customerPartNumber, orgID, clientLabel string)
	// suggest returns up to limit mappings whose description or customer part
	// number contains the query string (case-insensitive). Used by the in-cell
	// `?` popover in the BomTable — tight typeahead, narrow scope.
	suggest(query, orgID string, limit int) []*Mapping
	// search returns mappings matching the query across description, customer
	// P/N, internal P/N, and manufacturer P/N (all case-insensitive substring).
	// When clientFilter is non-empty, only that bucket is searched. Used by
	// the on-demand mapping search UI in the top nav.
	search(query, orgID, clientFilter string, limit int) []*Mapping
	// deleteByID removes the mapping with the given id, scoped to orgID.
	// Returns (true, nil) if a row was removed; (false, nil) if no matching
	// row existed; (false, err) on a DB error. The org scope is enforced so
	// callers cannot delete mappings owned by another org by guessing an id.
	deleteByID(id, orgID string) (bool, error)
}

// ClientMappingSummary is a row in the clients-list response: a label plus
// the number of mappings stored under it. Empty label = generic / untagged bucket.
type ClientMappingSummary struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// orgScopedMappings binds a mappingRepository to a fixed (orgID, clientLabel)
// pair, satisfying the mappingReader interface used by the analysis pipeline.
// Lookup is two-tier: prefer the client-scoped bucket, fall back to generic.
type orgScopedMappings struct {
	repo        mappingRepository
	orgID       string
	clientLabel string
}

func (o *orgScopedMappings) lookup(cpn string) (*Mapping, bool) {
	if o.clientLabel != "" {
		if m, ok := o.repo.lookupClient(cpn, o.orgID, o.clientLabel); ok {
			return m, true
		}
	}
	return o.repo.lookupClient(cpn, o.orgID, "")
}

func (o *orgScopedMappings) touchLastUsed(cpn, clientLabel string) {
	o.repo.touchLastUsed(cpn, o.orgID, clientLabel)
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
	clientLabel := normClientLabel(m.ClientLabel)
	return r.db.QueryRow(`
		INSERT INTO mappings
			(organization_id, client_label, customer_part_number, internal_part_number,
			 manufacturer_part_number, description, source, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (organization_id, client_label, customer_part_number) DO UPDATE SET
			internal_part_number     = EXCLUDED.internal_part_number,
			manufacturer_part_number = EXCLUDED.manufacturer_part_number,
			description              = EXCLUDED.description,
			source                   = EXCLUDED.source,
			confidence               = EXCLUDED.confidence,
			updated_at               = now()
		RETURNING id, created_at, updated_at, last_used_at`,
		orgID, clientLabel, key,
		m.InternalPartNumber, m.ManufacturerPartNumber, m.Description,
		m.Source, m.Confidence,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.LastUsedAt)
}

// lookup returns the generic-bucket mapping for the given CPN. This is the
// pre-client-tagging behaviour and remains the back-compat entry point.
// Callers that know a client label should call lookupClient directly.
func (r *pgMappingRepository) lookup(cpn, orgID string) (*Mapping, bool) {
	return r.lookupClient(cpn, orgID, "")
}

func (r *pgMappingRepository) lookupClient(cpn, orgID, clientLabel string) (*Mapping, bool) {
	var m Mapping
	err := r.db.QueryRow(`
		SELECT id, organization_id, client_label, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1 AND client_label = $2 AND customer_part_number = $3`,
		orgID, normClientLabel(clientLabel), normKey(cpn),
	).Scan(&m.ID, &m.OrganizationID, &m.ClientLabel, &m.CustomerPartNumber, &m.InternalPartNumber,
		&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
		&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		log.Printf("mapping lookupClient error: %v", err)
		return nil, false
	}
	return &m, true
}

func (r *pgMappingRepository) all(orgID string) []*Mapping {
	rows, err := r.db.Query(`
		SELECT id, organization_id, client_label, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1
		ORDER BY client_label, customer_part_number`,
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
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.ClientLabel, &m.CustomerPartNumber, &m.InternalPartNumber,
			&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
			&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			log.Printf("mapping scan error: %v", err)
			continue
		}
		result = append(result, &m)
	}
	return result
}

func (r *pgMappingRepository) clients(orgID string) []ClientMappingSummary {
	rows, err := r.db.Query(`
		SELECT client_label, COUNT(*) AS count
		FROM mappings
		WHERE organization_id = $1
		GROUP BY client_label
		ORDER BY client_label`,
		orgID,
	)
	if err != nil {
		log.Printf("mapping clients error: %v", err)
		return nil
	}
	defer rows.Close()
	var result []ClientMappingSummary
	for rows.Next() {
		var s ClientMappingSummary
		if err := rows.Scan(&s.Label, &s.Count); err != nil {
			log.Printf("mapping clients scan error: %v", err)
			continue
		}
		result = append(result, s)
	}
	return result
}

func (r *pgMappingRepository) suggest(query, orgID string, limit int) []*Mapping {
	if strings.TrimSpace(query) == "" {
		return []*Mapping{}
	}
	pattern := "%" + escapeLIKE(strings.ToLower(query)) + "%"
	rows, err := r.db.Query(`
		SELECT id, organization_id, client_label, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1
		  AND (LOWER(description) LIKE $2 ESCAPE '\'
		    OR LOWER(customer_part_number) LIKE $2 ESCAPE '\')
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
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.ClientLabel, &m.CustomerPartNumber, &m.InternalPartNumber,
			&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
			&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			log.Printf("mapping suggest scan error: %v", err)
			continue
		}
		result = append(result, &m)
	}
	return result
}

func (r *pgMappingRepository) search(query, orgID, clientFilter string, limit int) []*Mapping {
	if strings.TrimSpace(query) == "" {
		return []*Mapping{}
	}
	pattern := "%" + escapeLIKE(strings.ToLower(query)) + "%"
	args := []any{orgID, pattern}
	sql := `
		SELECT id, organization_id, client_label, customer_part_number, internal_part_number,
		       manufacturer_part_number, description, source, confidence,
		       last_used_at, created_at, updated_at
		FROM mappings
		WHERE organization_id = $1
		  AND (LOWER(description) LIKE $2 ESCAPE '\'
		    OR LOWER(customer_part_number) LIKE $2 ESCAPE '\'
		    OR LOWER(internal_part_number) LIKE $2 ESCAPE '\'
		    OR LOWER(manufacturer_part_number) LIKE $2 ESCAPE '\')`
	if clientFilter != "" {
		args = append(args, normClientLabel(clientFilter))
		sql += ` AND client_label = $3`
	}
	args = append(args, limit)
	sql += `
		ORDER BY last_used_at DESC
		LIMIT $` + fmtArgIdx(len(args))

	rows, err := r.db.Query(sql, args...)
	if err != nil {
		log.Printf("mapping search error: %v", err)
		return []*Mapping{}
	}
	defer rows.Close()
	var result []*Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.ClientLabel, &m.CustomerPartNumber, &m.InternalPartNumber,
			&m.ManufacturerPartNumber, &m.Description, &m.Source, &m.Confidence,
			&m.LastUsedAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			log.Printf("mapping search scan error: %v", err)
			continue
		}
		result = append(result, &m)
	}
	return result
}

func fmtArgIdx(i int) string { return fmt.Sprintf("%d", i) }

func (r *pgMappingRepository) deleteByID(id, orgID string) (bool, error) {
	res, err := r.db.Exec(
		`DELETE FROM mappings WHERE id = $1 AND organization_id = $2`,
		id, orgID,
	)
	if err != nil {
		return false, fmt.Errorf("mapping delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mapping delete RowsAffected: %w", err)
	}
	return n > 0, nil
}

func (r *pgMappingRepository) touchLastUsed(cpn, orgID, clientLabel string) {
	_, err := r.db.Exec(`
		UPDATE mappings SET last_used_at = now()
		WHERE organization_id = $1 AND client_label = $2 AND customer_part_number = $3`,
		orgID, normClientLabel(clientLabel), normKey(cpn),
	)
	if err != nil {
		log.Printf("touchLastUsed error: %v", err)
	}
}

func normKey(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// normClientLabel normalises a client label for case-insensitive equality.
// Display form is preserved at the call site; this is only used for lookup keys.
func normClientLabel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// escapeLIKE escapes the SQL LIKE metacharacters (%, _, \) so user input is
// treated literally. Real customer part numbers commonly contain underscores
// (CBL_RED_035), which without escaping would match any single character.
// Use with `LIKE $N ESCAPE '\'` at the call site.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLIKE(s string) string { return likeEscaper.Replace(s) }
