package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSearchServer(t *testing.T) (*server, string) {
	t.Helper()
	srv, token := newSettingsServer(t)
	store := newTestMappings()
	seeds := []*Mapping{
		{ClientLabel: "acme",       CustomerPartNumber: "ACME-001", InternalPartNumber: "W-R-035", ManufacturerPartNumber: "MPN-RED-035", Description: "0.35mm red wire"},
		{ClientLabel: "acme",       CustomerPartNumber: "ACME-002", InternalPartNumber: "W-B-035", ManufacturerPartNumber: "MPN-BLK-035", Description: "0.35mm black wire"},
		{ClientLabel: "beechcraft", CustomerPartNumber: "BC-RED-1", InternalPartNumber: "W-R-035", ManufacturerPartNumber: "MPN-RED-035", Description: "Red 0.35 wire"},
		{ClientLabel: "",           CustomerPartNumber: "GENERIC-1", InternalPartNumber: "F-005A", ManufacturerPartNumber: "FUSE-MFR-5A", Description: "5A blade fuse"},
	}
	for _, m := range seeds {
		_ = store.save(m, "org-1")
	}
	srv.mappings = store
	return srv, token
}

// Search must match against MPN — the most common starting point for an
// operator's "have I seen this part?" query, read off a drawing or datasheet.
// The current suggest endpoint does not search MPN; search must.
func TestSearchMappings_MatchesByMPN(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=MPN-RED", "", token)
	w := httptest.NewRecorder()

	srv.searchMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	assert.Len(t, results, 2, "MPN-RED-035 exists under both acme and beechcraft")
}

// Search must also match by internal P/N — operators sometimes ask
// "who uses our W-R-035?" working backwards from an internal code.
func TestSearchMappings_MatchesByInternalPN(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=W-R-035", "", token)
	w := httptest.NewRecorder()

	srv.searchMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	assert.Len(t, results, 2)
}

// Search must continue to match by customer P/N and description (existing
// suggest behaviour preserved in the broader endpoint).
func TestSearchMappings_MatchesByCustomerPNAndDescription(t *testing.T) {
	srv, token := newSearchServer(t)

	cases := []struct {
		query    string
		expected int
		label    string
	}{
		{"ACME-001", 1, "customer P/N exact"},
		{"acme", 2, "customer P/N substring (both ACME-001 and ACME-002)"},
		{"red", 2, "description substring (acme red wire + beechcraft red 0.35 wire)"},
		{"fuse", 1, "description substring matches the generic fuse"},
	}

	for _, tc := range cases {
		req := authedRequest(http.MethodGet, "/api/mappings/search?q="+tc.query, "", token)
		w := httptest.NewRecorder()
		srv.searchMappings(w, req)
		require.Equal(t, http.StatusOK, w.Code, tc.label)
		var results []*Mapping
		require.NoError(t, json.NewDecoder(w.Body).Decode(&results), tc.label)
		assert.GreaterOrEqual(t, len(results), tc.expected, "%s: query=%q got %d results", tc.label, tc.query, len(results))
	}
}

// Results must include client_label so the UI can disambiguate the same CPN
// or MPN appearing under different clients — the core value of the search.
func TestSearchMappings_ResultsIncludeClientLabel(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=MPN-RED-035", "", token)
	w := httptest.NewRecorder()
	srv.searchMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	require.Len(t, results, 2)

	labels := map[string]bool{}
	for _, m := range results {
		labels[m.ClientLabel] = true
	}
	assert.True(t, labels["acme"], "acme bucket result must surface its client_label")
	assert.True(t, labels["beechcraft"], "beechcraft bucket result must surface its client_label")
}

// Optional client= filter narrows results to a single bucket.
func TestSearchMappings_ClientFilter(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=W-R-035&client=acme", "", token)
	w := httptest.NewRecorder()
	srv.searchMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	require.Len(t, results, 1)
	assert.Equal(t, "acme", results[0].ClientLabel)
}

// Empty / whitespace query returns an empty result list, not 400. The frontend
// debounces — clearing the input shouldn't surface an error.
func TestSearchMappings_EmptyQueryReturnsEmpty(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/search?q=", "", token)
	w := httptest.NewRecorder()
	srv.searchMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	assert.Empty(t, results)
}

// Bundled bug fix: the existing /api/mappings/suggest endpoint historically
// did not return client_label. Once a mapping is stored under a non-generic
// bucket, the suggest result's ClientLabel must reflect the source bucket so
// the in-cell `?` popover can render disambiguating context.
func TestSuggestMappings_ResultIncludesClientLabel(t *testing.T) {
	srv, token := newSearchServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=red", "", token)
	w := httptest.NewRecorder()
	srv.suggestMappings(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var results []*Mapping
	require.NoError(t, json.NewDecoder(w.Body).Decode(&results))
	require.NotEmpty(t, results)
	hasClient := false
	for _, m := range results {
		if m.ClientLabel != "" {
			hasClient = true
			break
		}
	}
	assert.True(t, hasClient, "at least one of the seeded acme/beechcraft results must surface a non-empty client_label")
}
