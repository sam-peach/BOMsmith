package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
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

// ── inMemoryMappingRepository ─────────────────────────────────────────────────

// inMemoryMappingRepository wraps *mappingStore and implements mappingRepository
// by ignoring orgID. Used when DATABASE_URL is not set (local development).
type inMemoryMappingRepository struct {
	store *mappingStore
}

func (r *inMemoryMappingRepository) save(m *Mapping, _ string) error {
	return r.store.save(m)
}

func (r *inMemoryMappingRepository) lookup(cpn, _ string) (*Mapping, bool) {
	return r.store.lookup(cpn)
}

func (r *inMemoryMappingRepository) all(_ string) []*Mapping {
	return r.store.all()
}

func (r *inMemoryMappingRepository) touchLastUsed(cpn, _ string) {
	r.store.touchLastUsed(cpn)
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

type mappingStore struct {
	mu       sync.RWMutex
	data     map[string]*Mapping // keyed by normKey(customerPartNumber)
	filePath string
}

func newMappingStore(filePath string) (*mappingStore, error) {
	ms := &mappingStore{
		data:     make(map[string]*Mapping),
		filePath: filePath,
	}
	if err := ms.load(); err != nil {
		return nil, err
	}
	return ms, nil
}

// load reads the JSON file on startup. A missing file is not an error.
func (s *mappingStore) load() error {
	b, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read mappings file: %w", err)
	}
	var list []*Mapping
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse mappings file: %w", err)
	}
	for _, m := range list {
		if k := normKey(m.CustomerPartNumber); k != "" {
			s.data[k] = m
		}
	}
	return nil
}

// persist writes the current state to the JSON file atomically.
// No-ops when filePath is empty (in-memory / test mode).
// Must be called with the write lock held (or from a method that holds it).
func (s *mappingStore) persist() error {
	if s.filePath == "" {
		return nil
	}
	list := make([]*Mapping, 0, len(s.data))
	for _, m := range s.data {
		list = append(list, m)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func normKey(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// save creates or updates a mapping and persists to disk.
func (s *mappingStore) save(m *Mapping) error {
	key := normKey(m.CustomerPartNumber)
	if key == "" {
		return fmt.Errorf("customerPartNumber is required")
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.data[key]; ok {
		// Preserve creation timestamp; update everything else.
		m.ID = existing.ID
		m.CreatedAt = existing.CreatedAt
	} else {
		if m.ID == "" {
			m.ID = newID()
		}
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.LastUsedAt.IsZero() {
		m.LastUsedAt = now
	}
	s.data[key] = m
	return s.persist()
}

// touchLastUsed updates LastUsedAt without changing other fields. Best-effort (no error returned).
func (s *mappingStore) touchLastUsed(customerPartNumber string) {
	key := normKey(customerPartNumber)
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[key]; ok {
		m.LastUsedAt = time.Now().UTC()
		_ = s.persist()
	}
}

func (s *mappingStore) lookup(customerPartNumber string) (*Mapping, bool) {
	key := normKey(customerPartNumber)
	if key == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.data[key]
	return m, ok
}

func (s *mappingStore) all() []*Mapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Mapping, 0, len(s.data))
	for _, m := range s.data {
		out = append(out, m)
	}
	return out
}
