package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── client-scoped lookup behaviour ────────────────────────────────────────────

// When a drawing is tagged with a client and an exact mapping exists under
// that client, the client-scoped mapping wins over any generic mapping.
func TestApplyMapping_ClientScoped_PrefersClientBucket(t *testing.T) {
	ms := newTestMappingReaderForClient("acme")
	ms.add(&Mapping{CustomerPartNumber: "X-001", InternalPartNumber: "SC-GENERIC"})
	ms.add(&Mapping{CustomerPartNumber: "X-001", InternalPartNumber: "SC-ACME", ClientLabel: "acme"})

	row := &BOMRow{CustomerPartNumber: "X-001"}
	applyMapping(row, ms)

	assert.Equal(t, "SC-ACME", row.InternalPartNumber,
		"a drawing tagged 'acme' must prefer the acme-scoped mapping over the generic one")
	assert.Contains(t, row.ConfirmedFields, "internalPartNumber")
}

// When a drawing is tagged with a client but only a generic mapping exists,
// the generic mapping is the fallback — the system still finds the answer.
func TestApplyMapping_ClientScoped_FallsBackToGeneric(t *testing.T) {
	ms := newTestMappingReaderForClient("acme")
	ms.add(&Mapping{CustomerPartNumber: "X-001", InternalPartNumber: "SC-GENERIC"})

	row := &BOMRow{CustomerPartNumber: "X-001"}
	applyMapping(row, ms)

	assert.Equal(t, "SC-GENERIC", row.InternalPartNumber,
		"generic bucket is the fallback when no client-scoped mapping exists")
}

// A drawing with no client tag only hits the generic bucket. A mapping
// stored under a specific client must not leak into untagged drawings.
func TestApplyMapping_NoClientTag_DoesNotSeeClientBucket(t *testing.T) {
	ms := newTestMappingReaderForClient("") // untagged drawing
	ms.add(&Mapping{CustomerPartNumber: "X-001", InternalPartNumber: "SC-ACME", ClientLabel: "acme"})

	row := &BOMRow{CustomerPartNumber: "X-001"}
	applyMapping(row, ms)

	assert.Empty(t, row.InternalPartNumber,
		"untagged drawings only see generic mappings — client-scoped entries are isolated")
}

// Two clients can use the same CPN for different parts without collision.
// This is the bug the per-client tagging is designed to fix.
func TestApplyMapping_ClientScoped_IsolatesClients(t *testing.T) {
	// Both clients' mappings live in one store; the reader is scoped to one client per drawing.
	store := newTestMappingStore()
	store.add(&Mapping{CustomerPartNumber: "P/N 1234", InternalPartNumber: "RED-WIRE", ClientLabel: "acme"})
	store.add(&Mapping{CustomerPartNumber: "P/N 1234", InternalPartNumber: "BLUE-CONNECTOR", ClientLabel: "beechcraft"})

	rowA := &BOMRow{CustomerPartNumber: "P/N 1234"}
	applyMapping(rowA, store.scope("acme"))
	assert.Equal(t, "RED-WIRE", rowA.InternalPartNumber)

	rowB := &BOMRow{CustomerPartNumber: "P/N 1234"}
	applyMapping(rowB, store.scope("beechcraft"))
	assert.Equal(t, "BLUE-CONNECTOR", rowB.InternalPartNumber)
}

// ── import endpoint ───────────────────────────────────────────────────────────

// POST /api/mappings/import persists a batch of rows under a named client label.
func TestMappingsImport_PersistsUnderClient(t *testing.T) {
	srv, token := newSettingsServer(t)

	body, _ := json.Marshal(map[string]any{
		"clientLabel": "Acme Aerospace",
		"rows": []map[string]string{
			{"customerPartNumber": "ACME-001", "internalPartNumber": "SC-001", "description": "Red wire"},
			{"customerPartNumber": "ACME-002", "internalPartNumber": "SC-002", "description": "Blue wire"},
		},
	})
	req := authedRequest(http.MethodPost, "/api/mappings/import", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.importMappings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]int
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 2, result["saved"])

	m, ok := srv.mappings.lookupClient("ACME-001", "org-1", "Acme Aerospace")
	require.True(t, ok, "imported mapping must be retrievable under its client label")
	assert.Equal(t, "SC-001", m.InternalPartNumber)
	assert.Equal(t, "excel-import", m.Source)
}

// An import that re-sends an existing CPN under the same client overwrites
// the prior value and reports the overwrite count separately from new rows.
func TestMappingsImport_Overwrites_WithVisibleCount(t *testing.T) {
	srv, token := newSettingsServer(t)
	_ = srv.mappings.save(&Mapping{
		CustomerPartNumber: "ACME-001",
		InternalPartNumber: "SC-OLD",
		ClientLabel:        "acme",
		Source:             "excel-import",
	}, "org-1")

	body, _ := json.Marshal(map[string]any{
		"clientLabel": "acme",
		"rows": []map[string]string{
			{"customerPartNumber": "ACME-001", "internalPartNumber": "SC-NEW"},
			{"customerPartNumber": "ACME-002", "internalPartNumber": "SC-002"},
		},
	})
	req := authedRequest(http.MethodPost, "/api/mappings/import", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.importMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var result map[string]int
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 1, result["saved"], "one genuinely new row")
	assert.Equal(t, 1, result["overwritten"], "one existing row updated — visible in the result so operators see what changed")

	m, _ := srv.mappings.lookupClient("ACME-001", "org-1", "acme")
	assert.Equal(t, "SC-NEW", m.InternalPartNumber, "client-supplied data is authoritative — overwrite, don't preserve old value")
}

// GET /api/mappings/clients returns the distinct client labels in the org
// along with mapping counts — the data the UI uses to render the dropdown.
func TestMappingsClients_ReturnsDistinctLabelsWithCounts(t *testing.T) {
	srv, token := newSettingsServer(t)
	_ = srv.mappings.save(&Mapping{CustomerPartNumber: "A1", InternalPartNumber: "I1", ClientLabel: "acme"}, "org-1")
	_ = srv.mappings.save(&Mapping{CustomerPartNumber: "A2", InternalPartNumber: "I2", ClientLabel: "acme"}, "org-1")
	_ = srv.mappings.save(&Mapping{CustomerPartNumber: "B1", InternalPartNumber: "I3", ClientLabel: "beechcraft"}, "org-1")
	_ = srv.mappings.save(&Mapping{CustomerPartNumber: "G1", InternalPartNumber: "I4"}, "org-1") // generic

	req := authedRequest(http.MethodGet, "/api/mappings/clients", "", token)
	w := httptest.NewRecorder()
	srv.listMappingClients(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var clients []struct {
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&clients))

	byLabel := map[string]int{}
	for _, c := range clients {
		byLabel[c.Label] = c.Count
	}
	assert.Equal(t, 2, byLabel["acme"])
	assert.Equal(t, 1, byLabel["beechcraft"])
	assert.Equal(t, 1, byLabel[""], "generic / untagged bucket is also returned")
}

// ── auto-learn carries the document's client label ────────────────────────────

// When the operator confirms an IPN on a tagged drawing and saves, the
// auto-learned mapping inherits the drawing's client label.
func TestSaveBOM_AutoLearn_InheritsDocClientLabel(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{
		ID:          "doc-cl-1",
		Filename:    "acme-drawing.pdf",
		BOMRows:     []BOMRow{},
		ClientLabel: "Acme Aerospace",
	}
	srv.store.save(doc)

	rows := []BOMRow{{
		ID:                 "r1",
		LineNumber:         1,
		CustomerPartNumber: "ACME-NEW",
		InternalPartNumber: "SC-NEW",
		Quantity:           Quantity{Raw: "1", Flags: []string{}},
		Flags:              []string{},
		ConfirmedFields:    []string{"internalPartNumber"},
	}}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-cl-1/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-cl-1")
	w := httptest.NewRecorder()
	srv.saveBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	m, ok := srv.mappings.lookupClient("ACME-NEW", "org-1", "Acme Aerospace")
	require.True(t, ok, "auto-learn must persist under the drawing's client label")
	assert.Equal(t, "SC-NEW", m.InternalPartNumber)
}

// ── test helpers ──────────────────────────────────────────────────────────────

// newTestMappingReaderForClient builds a fakeMappingReader bound to a single
// client label, mirroring how orgScopedMappings is constructed per-document
// in the real handler. The empty string means "no client tag" — only the
// generic bucket is visible.
func newTestMappingReaderForClient(clientLabel string) *clientScopedFakeReader {
	return &clientScopedFakeReader{store: newTestMappingStore(), clientLabel: clientLabel}
}

// testMappingStore is a tiny in-memory store keyed by (clientLabel, normCPN)
// so tests can model isolation between clients without spinning up Postgres.
type testMappingStore struct {
	data map[string]*Mapping // key: clientLabel + "|" + normCPN
}

func newTestMappingStore() *testMappingStore {
	return &testMappingStore{data: map[string]*Mapping{}}
}

func (s *testMappingStore) add(m *Mapping) {
	s.data[s.key(m.ClientLabel, m.CustomerPartNumber)] = m
}

func (s *testMappingStore) key(clientLabel, cpn string) string {
	return strings.ToLower(strings.TrimSpace(clientLabel)) + "|" + normKey(cpn)
}

func (s *testMappingStore) scope(clientLabel string) *clientScopedFakeReader {
	return &clientScopedFakeReader{store: s, clientLabel: clientLabel}
}

// clientScopedFakeReader implements mappingReader with two-tier lookup:
// (clientLabel, cpn) first, then (generic, cpn) as fallback.
type clientScopedFakeReader struct {
	store       *testMappingStore
	clientLabel string
}

func (r *clientScopedFakeReader) add(m *Mapping) { r.store.add(m) }

func (r *clientScopedFakeReader) lookup(cpn string) (*Mapping, bool) {
	if r.clientLabel != "" {
		if m, ok := r.store.data[r.store.key(r.clientLabel, cpn)]; ok {
			return m, true
		}
	}
	if m, ok := r.store.data[r.store.key("", cpn)]; ok {
		return m, true
	}
	return nil, false
}

func (r *clientScopedFakeReader) touchLastUsed(_ string) {}
