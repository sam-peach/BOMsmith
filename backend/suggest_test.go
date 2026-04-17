package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSuggestServer(t *testing.T) (*server, string) {
	t.Helper()
	srv, token := newSettingsServer(t)

	// Seed a few mappings to search over.
	seeds := []*Mapping{
		{CustomerPartNumber: "CBL-RED-0.35", Description: "0.35mm red wire", InternalPartNumber: "W-R-035"},
		{CustomerPartNumber: "CBL-BLK-0.35", Description: "0.35mm black wire", InternalPartNumber: "W-B-035"},
		{CustomerPartNumber: "CON-12P-DEUTSCH", Description: "12-pin Deutsch connector DT06-12S", InternalPartNumber: "C-DT-12"},
		{CustomerPartNumber: "HS-3MM-BLK", Description: "3mm black heatshrink sleeving", InternalPartNumber: "HS-3-B"},
		{CustomerPartNumber: "FUSE-5A", Description: "5A blade fuse", InternalPartNumber: "F-005A"},
	}
	store := newTestMappings()
	for _, m := range seeds {
		_ = store.save(m, "org-1")
	}
	srv.mappings = store
	return srv, token
}

// TestSuggest_ByDescription verifies substring match on description.
func TestSuggest_ByDescription(t *testing.T) {
	srv, token := newSuggestServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=wire", "", token)
	w := httptest.NewRecorder()

	srv.suggestMappings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []*Mapping
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 wire matches, got %d", len(results))
	}
	// Both CBL-RED and CBL-BLK should appear.
	var cpns []string
	for _, m := range results {
		cpns = append(cpns, m.CustomerPartNumber)
	}
	assertContains(t, cpns, "CBL-RED-0.35")
	assertContains(t, cpns, "CBL-BLK-0.35")
}

// TestSuggest_ByCustomerPartNumber verifies substring match on customer P/N.
func TestSuggest_ByCustomerPartNumber(t *testing.T) {
	srv, token := newSuggestServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=deutsch", "", token)
	w := httptest.NewRecorder()

	srv.suggestMappings(w, req)

	var results []*Mapping
	_ = json.NewDecoder(w.Body).Decode(&results)

	if len(results) < 1 {
		t.Fatalf("expected at least 1 Deutsch connector match, got %d", len(results))
	}
	var cpns []string
	for _, m := range results {
		cpns = append(cpns, m.CustomerPartNumber)
	}
	assertContains(t, cpns, "CON-12P-DEUTSCH")
}

// TestSuggest_CaseInsensitive verifies the search is case-insensitive.
func TestSuggest_CaseInsensitive(t *testing.T) {
	srv, token := newSuggestServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=HEATSHRINK", "", token)
	w := httptest.NewRecorder()

	srv.suggestMappings(w, req)

	var results []*Mapping
	_ = json.NewDecoder(w.Body).Decode(&results)

	if len(results) < 1 {
		t.Fatalf("expected heatshrink match, got 0")
	}
}

// TestSuggest_EmptyQuery returns empty array (not an error).
func TestSuggest_EmptyQuery(t *testing.T) {
	srv, token := newSuggestServer(t)
	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=", "", token)
	w := httptest.NewRecorder()

	srv.suggestMappings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var results []*Mapping
	_ = json.NewDecoder(w.Body).Decode(&results)
	if results == nil {
		t.Error("expected non-nil array for empty query")
	}
}

// TestSuggest_LimitRespected verifies at most 5 results are returned.
func TestSuggest_LimitRespected(t *testing.T) {
	srv, token := newSettingsServer(t)
	// Seed 10 matching mappings.
	store := newTestMappings()
	for i := 0; i < 10; i++ {
		_ = store.save(&Mapping{
			CustomerPartNumber: fmt.Sprintf("WIRE-%02d", i),
			Description:        "copper wire conductor",
		}, "org-1")
	}
	srv.mappings = store

	req := authedRequest(http.MethodGet, "/api/mappings/suggest?q=wire", "", token)
	w := httptest.NewRecorder()
	srv.suggestMappings(w, req)

	var results []*Mapping
	_ = json.NewDecoder(w.Body).Decode(&results)
	if len(results) > 5 {
		t.Errorf("expected at most 5 results, got %d", len(results))
	}
}

func assertContains(t *testing.T, slice []string, item string) {
	t.Helper()
	for _, s := range slice {
		if s == item {
			return
		}
	}
	t.Errorf("expected %q to be in %v", item, slice)
}
