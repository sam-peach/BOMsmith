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

// ── C1 — saveMapping must persist into the client bucket from the body ────────

// When the frontend sends a Mapping with clientLabel set, saveMapping must
// store the row in that client's bucket — not the generic bucket. This is the
// core bug from the audit: the MappingsPage edit and BomTable manual-save
// flows both routed Acme edits into the generic bucket because clientLabel
// was missing from the wire format.
func TestSaveMapping_PersistsClientLabel(t *testing.T) {
	srv, token := newSettingsServer(t)

	body := `{
		"clientLabel": "Acme Aerospace",
		"customerPartNumber": "ACME-EDIT-1",
		"internalPartNumber": "W-EDIT-1",
		"manufacturerPartNumber": "MPN-EDIT-1",
		"description": "Edited via saveMapping",
		"source": "manual"
	}`
	req := authedRequest(http.MethodPost, "/api/mappings", body, token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.saveMapping(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Must land in the Acme bucket — NOT the generic one.
	m, ok := srv.mappings.lookupClient("ACME-EDIT-1", "org-1", "Acme Aerospace")
	require.True(t, ok, "mapping must be retrievable from the Acme bucket")
	assert.Equal(t, "W-EDIT-1", m.InternalPartNumber)

	_, generic := srv.mappings.lookupClient("ACME-EDIT-1", "org-1", "")
	assert.False(t, generic, "mapping must NOT have leaked into the generic bucket")
}

// Updating an existing per-client mapping via saveMapping (the MappingsPage
// inline-edit flow) must overwrite the same row, not create a generic dup.
func TestSaveMapping_UpdatesInPlaceForClientBucket(t *testing.T) {
	srv, token := newSettingsServer(t)
	require.NoError(t, srv.mappings.save(&Mapping{
		ClientLabel:        "Acme",
		CustomerPartNumber: "ACME-001",
		InternalPartNumber: "W-OLD",
		Source:             "excel-import",
	}, "org-1"))

	body := `{
		"clientLabel": "Acme",
		"customerPartNumber": "ACME-001",
		"internalPartNumber": "W-NEW",
		"source": "manual"
	}`
	req := authedRequest(http.MethodPost, "/api/mappings", body, token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.saveMapping(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	m, _ := srv.mappings.lookupClient("ACME-001", "org-1", "Acme")
	assert.Equal(t, "W-NEW", m.InternalPartNumber, "in-place edit must overwrite")

	_, generic := srv.mappings.lookupClient("ACME-001", "org-1", "")
	assert.False(t, generic, "no shadow row in the generic bucket")
}

// ── C2 — touchLastUsed must scope by client_label ─────────────────────────────

// Two clients can share a CPN; touching one must not bump the other's
// last_used_at. The pre-fix touchLastUsed ignored client_label entirely.
// (We call touchLastUsed directly so the test is deterministic — the
// applyMapping goroutine wrapper is racy on the assertion.)
func TestTouchLastUsed_ScopedToClientLabel(t *testing.T) {
	store := newTestMappings()
	require.NoError(t, store.save(&Mapping{
		ClientLabel:        "acme",
		CustomerPartNumber: "X-001",
		InternalPartNumber: "W-A",
	}, "org-1"))
	require.NoError(t, store.save(&Mapping{
		ClientLabel:        "beechcraft",
		CustomerPartNumber: "X-001",
		InternalPartNumber: "W-B",
	}, "org-1"))

	bcBefore, _ := store.lookupClient("X-001", "org-1", "beechcraft")
	beechcraftTimestamp := bcBefore.LastUsedAt

	store.touchLastUsed("X-001", "org-1", "acme")

	acme, _ := store.lookupClient("X-001", "org-1", "acme")
	bc, _ := store.lookupClient("X-001", "org-1", "beechcraft")
	assert.True(t, acme.LastUsedAt.After(beechcraftTimestamp),
		"acme last_used_at must be bumped after touching acme bucket")
	assert.Equal(t, beechcraftTimestamp, bc.LastUsedAt,
		"beechcraft last_used_at must not be bumped by an acme touch")
}

// ── H2 — exact-MPN catalog hit promotes MPN to Confirmed even when filled ────

// When the LLM extracted an MPN and the catalog has an exact match on that
// MPN, the value is identity-confirmed (the catalog entry traces to a stored
// mapping which traces to a human declaration). The MPN cell should be
// promoted to Confirmed alongside the IPN — operator should not have to
// click ✓ on a value the system just identity-matched.
func TestParseBOMRows_ExactMPNCatalogMatch_PromotesPrePopulatedMPN(t *testing.T) {
	catalog := &fakeCatalogReader{
		byMPN: map[string]*CatalogPart{
			"MPN-EXACT": {
				ID:                     "cat-2",
				InternalPartNumber:     "SC-EXACT",
				ManufacturerPartNumber: "MPN-EXACT",
				Fingerprint:            PartFingerprint{Type: "wire", Diameter: "0.20mm"},
			},
		},
	}

	// LLM extracts MPN from the drawing's Part Reference Table.
	input := `[{"rawLabel":"1","description":"Wire","rawQuantity":"1","unit":"M","customerPartNumber":"","manufacturerPartNumber":"MPN-EXACT","supplierReference":"","notes":"","confidence":0.9,"flags":[]}]`

	rows, _, err := parseBOMRows(input, nil, catalog)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "SC-EXACT", rows[0].InternalPartNumber)
	assert.Contains(t, rows[0].ConfirmedFields, "internalPartNumber")
	assert.Equal(t, "MPN-EXACT", rows[0].ManufacturerPartNumber)
	assert.Contains(t, rows[0].ConfirmedFields, "manufacturerPartNumber",
		"MPN already in cell + exact-MPN catalog identity match = must be Confirmed too")
}

// ── H3 — LIKE metacharacter escaping in suggest + search ──────────────────────

// Operator-typed `_` is common in real CPNs (e.g. CBL_RED_035) but is a LIKE
// metacharacter that means "match any single character". Without escaping,
// a search for "CBL_RED" would also match "CBLxRED", "CBL-RED", etc. Fix
// must escape `_`, `%`, and `\` before building the LIKE pattern.
func TestSearch_LIKEEscaping(t *testing.T) {
	srv, token := newSettingsServer(t)
	store := newTestMappings()
	// Two parts that differ only in a literal underscore vs a hyphen.
	require.NoError(t, store.save(&Mapping{
		CustomerPartNumber: "CBL_RED_035",
		InternalPartNumber: "W-UNDER",
	}, "org-1"))
	require.NoError(t, store.save(&Mapping{
		CustomerPartNumber: "CBL-RED-035",
		InternalPartNumber: "W-DASH",
	}, "org-1"))
	srv.mappings = store

	// Search for the literal underscore form. Must NOT match the hyphenated one.
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=CBL_RED", "", token)
	w := httptest.NewRecorder()
	srv.searchMappings(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	require.Len(t, results, 1, "underscore must be treated literally — not as a LIKE wildcard")
	assert.Equal(t, "W-UNDER", results[0].InternalPartNumber)
}

// Unit test for the escape helper itself — pg integration tests can't run
// in this suite without a live DB, so this is the load-bearing assertion
// that the LIKE-injection bug is actually fixed.
func TestEscapeLIKE(t *testing.T) {
	cases := []struct{ in, want string }{
		{`abc`, `abc`},
		{`CBL_RED`, `CBL\_RED`},   // underscore — must be escaped
		{`100%`, `100\%`},          // percent — must be escaped
		{`a\b`, `a\\b`},            // backslash — must be escaped (and goes first)
		{`A_B%C\D`, `A\_B\%C\\D`},
		{``, ``},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, escapeLIKE(tc.in), "input %q", tc.in)
	}
}

// Same escaping requirement applies to the older suggest endpoint.
func TestSuggest_LIKEEscaping(t *testing.T) {
	srv, token := newSettingsServer(t)
	store := newTestMappings()
	require.NoError(t, store.save(&Mapping{
		CustomerPartNumber: "CBL_RED_035",
		InternalPartNumber: "W-UNDER",
		Description:        "underscore wire",
	}, "org-1"))
	require.NoError(t, store.save(&Mapping{
		CustomerPartNumber: "CBL-RED-035",
		InternalPartNumber: "W-DASH",
		Description:        "hyphen wire",
	}, "org-1"))
	srv.mappings = store

	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=CBL_RED", "", token)
	w := httptest.NewRecorder()
	srv.suggestMappings(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	for _, m := range results {
		assert.True(t, strings.Contains(m.CustomerPartNumber, "CBL_RED"),
			"suggest must treat underscore literally: got %q", m.CustomerPartNumber)
	}
}
