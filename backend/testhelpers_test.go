package main

// testhelpers_test.go — in-memory fakes for use only in tests.
// These replace the old production in-memory implementations that were removed
// when the app moved to requiring Postgres for all deployments.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── memDocumentStore ──────────────────────────────────────────────────────────

type memDocumentStore struct {
	mu   sync.RWMutex
	docs map[string]*Document
}

func newTestStore() documentRepository {
	return &memDocumentStore{docs: make(map[string]*Document)}
}

func (s *memDocumentStore) save(doc *Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc.ID] = doc
}

func (s *memDocumentStore) get(id string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[id]
	if !ok {
		return nil, fmt.Errorf("document %q not found", id)
	}
	return doc, nil
}

func (s *memDocumentStore) list(orgID string) ([]*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Document
	for _, doc := range s.docs {
		if doc.OrganizationID == orgID {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (s *memDocumentStore) listByOrg(orgID string) ([]*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Document
	for _, doc := range s.docs {
		if doc.Status == StatusDone && doc.OrganizationID == orgID {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (s *memDocumentStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, id)
	return nil
}

func (s *memDocumentStore) resetAnalyzing() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range s.docs {
		if doc.Status == StatusAnalyzing {
			doc.Status = StatusError
			doc.ErrorMessage = "Analysis was interrupted by a server restart — please re-analyse."
		}
	}
	return nil
}

// ── fakeMappingRepository ─────────────────────────────────────────────────────

// fakeMappingRepository is a simple in-memory implementation of mappingRepository.
// Org scoping is ignored (single-org tests only).
type fakeMappingRepository struct {
	mu   sync.RWMutex
	data map[string]*Mapping
}

func newTestMappings() *fakeMappingRepository {
	return &fakeMappingRepository{data: make(map[string]*Mapping)}
}

func (r *fakeMappingRepository) save(m *Mapping, _ string) error {
	key := normKey(m.CustomerPartNumber)
	if key == "" {
		return fmt.Errorf("customerPartNumber is required")
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.data[key]; ok {
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
	r.data[key] = m
	return nil
}

func (r *fakeMappingRepository) lookup(cpn, _ string) (*Mapping, bool) {
	key := normKey(cpn)
	if key == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.data[key]
	return m, ok
}

func (r *fakeMappingRepository) all(_ string) []*Mapping {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Mapping, 0, len(r.data))
	for _, m := range r.data {
		out = append(out, m)
	}
	return out
}

func (r *fakeMappingRepository) touchLastUsed(cpn, _ string) {
	key := normKey(cpn)
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.data[key]; ok {
		m.LastUsedAt = time.Now().UTC()
	}
}

func (r *fakeMappingRepository) suggest(query, _ string, limit int) []*Mapping {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return []*Mapping{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Mapping
	for _, m := range r.data {
		if strings.Contains(strings.ToLower(m.Description), q) ||
			strings.Contains(strings.ToLower(m.CustomerPartNumber), q) {
			out = append(out, m)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// ── fakeMappingReader ─────────────────────────────────────────────────────────

// fakeMappingReader satisfies the mappingReader interface for analysis pipeline tests.
type fakeMappingReader struct {
	mu   sync.RWMutex
	data map[string]*Mapping
}

func newTestMappingReader() *fakeMappingReader {
	return &fakeMappingReader{data: make(map[string]*Mapping)}
}

func (r *fakeMappingReader) add(m *Mapping) {
	key := normKey(m.CustomerPartNumber)
	if key == "" {
		key = normKey(m.ManufacturerPartNumber)
	}
	if key == "" {
		return
	}
	if m.ID == "" {
		m.ID = newID()
	}
	r.mu.Lock()
	r.data[key] = m
	r.mu.Unlock()
}

func (r *fakeMappingReader) lookup(cpn string) (*Mapping, bool) {
	key := normKey(cpn)
	if key == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.data[key]
	return m, ok
}

func (r *fakeMappingReader) touchLastUsed(_ string) {}

// ── fakeMatchFeedbackRepository ───────────────────────────────────────────────

type fakeMatchFeedbackRepository struct {
	mu      sync.Mutex
	entries map[string][]*MatchFeedback
}

func newTestMatchFeedback() *fakeMatchFeedbackRepository {
	return &fakeMatchFeedbackRepository{entries: make(map[string][]*MatchFeedback)}
}

func (r *fakeMatchFeedbackRepository) record(fb *MatchFeedback, orgID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fb.ID == "" {
		fb.ID = newID()
	}
	fb.OrganizationID = orgID
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now().UTC()
	}
	r.entries[orgID] = append(r.entries[orgID], fb)
	return nil
}

func (r *fakeMatchFeedbackRepository) all(orgID string) []*MatchFeedback {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[orgID]
}
