package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSaveBOM_AutoLearnCreatesMapping verifies that when a BOM row with a
// customerPartNumber and internalPartNumber is saved, a mapping is
// automatically created (source="inferred") if none exists yet.
func TestSaveBOM_AutoLearnCreatesMapping(t *testing.T) {
	srv, token := newSettingsServer(t)
	// Seed a document.
	doc := &Document{ID: "doc-al-1", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                     "r1",
			LineNumber:             1,
			Description:            "Red wire 0.35mm",
			CustomerPartNumber:     "CBL-RED",
			InternalPartNumber:     "W-R-035",
			ManufacturerPartNumber: "MPN-123",
			Quantity:               Quantity{Raw: "5", Flags: []string{}},
			Flags:                  []string{},
			ConfirmedFields:        []string{"internalPartNumber"},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-al-1/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-al-1")
	w := httptest.NewRecorder()

	srv.saveBOM(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The mapping should now exist.
	m, ok := srv.mappings.lookup("CBL-RED", "org-1")
	if !ok {
		t.Fatal("expected mapping to be created after saveBOM with customerPartNumber + internalPartNumber")
	}
	if m.InternalPartNumber != "W-R-035" {
		t.Errorf("InternalPartNumber: want %q, got %q", "W-R-035", m.InternalPartNumber)
	}
	if m.ManufacturerPartNumber != "MPN-123" {
		t.Errorf("ManufacturerPartNumber: want %q, got %q", "MPN-123", m.ManufacturerPartNumber)
	}
	if m.Source != "inferred" {
		t.Errorf("Source: want %q, got %q", "inferred", m.Source)
	}
}

// TestSaveBOM_AutoLearnSkipsRowsWithoutCustomerPN verifies that rows without
// a customerPartNumber do not create mappings.
func TestSaveBOM_AutoLearnSkipsRowsWithoutCustomerPN(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{ID: "doc-al-2", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                 "r1",
			LineNumber:         1,
			Description:        "Red wire",
			CustomerPartNumber: "", // no CPN — should not create a mapping
			InternalPartNumber: "W-R-035",
			Quantity:           Quantity{Raw: "5", Flags: []string{}},
			Flags:              []string{},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-al-2/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-al-2")
	w := httptest.NewRecorder()

	srv.saveBOM(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	all := srv.mappings.all("org-1")
	if len(all) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(all))
	}
}

// TestSaveBOM_AutoLearnDoesNotOverwriteManualMapping verifies that a "manual"
// source mapping is never overwritten by an inferred one.
func TestSaveBOM_AutoLearnDoesNotOverwriteManualMapping(t *testing.T) {
	srv, token := newSettingsServer(t)
	// Pre-seed a manual mapping.
	_ = srv.mappings.save(&Mapping{
		CustomerPartNumber:     "CBL-RED",
		InternalPartNumber:     "W-R-MANUAL",
		ManufacturerPartNumber: "MPN-MANUAL",
		Source:                 "manual",
		Confidence:             1.0,
	}, "org-1")

	doc := &Document{ID: "doc-al-3", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                     "r1",
			LineNumber:             1,
			CustomerPartNumber:     "CBL-RED",
			InternalPartNumber:     "W-R-NEW",
			ManufacturerPartNumber: "MPN-NEW",
			Quantity:               Quantity{Raw: "1", Flags: []string{}},
			Flags:                  []string{},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-al-3/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-al-3")
	w := httptest.NewRecorder()
	srv.saveBOM(w, req)

	// The manual mapping must be preserved.
	m, ok := srv.mappings.lookup("CBL-RED", "org-1")
	if !ok {
		t.Fatal("mapping should still exist")
	}
	if m.InternalPartNumber != "W-R-MANUAL" {
		t.Errorf("manual mapping should not be overwritten: got %q", m.InternalPartNumber)
	}
	if m.Source != "manual" {
		t.Errorf("Source should remain %q, got %q", "manual", m.Source)
	}
}

// TestSaveBOM_AutoLearnUsesMPNWhenNoCPN verifies that when a row has no
// customerPartNumber but does have a manufacturerPartNumber + internalPartNumber,
// the mapping is saved keyed by the MPN.
func TestSaveBOM_AutoLearnUsesMPNWhenNoCPN(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{ID: "doc-mpn-1", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                     "r1",
			LineNumber:             1,
			Description:            "Molex connector",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "43640-0300",
			InternalPartNumber:     "CONN-001",
			Quantity:               Quantity{Raw: "1", Flags: []string{}},
			Flags:                  []string{},
			ConfirmedFields:        []string{"internalPartNumber"},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-mpn-1/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-mpn-1")
	w := httptest.NewRecorder()

	srv.saveBOM(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Mapping should be keyed by MPN.
	m, ok := srv.mappings.lookup("43640-0300", "org-1")
	if !ok {
		t.Fatal("expected mapping keyed by MPN to be created")
	}
	if m.InternalPartNumber != "CONN-001" {
		t.Errorf("InternalPartNumber: want %q, got %q", "CONN-001", m.InternalPartNumber)
	}
}

// TestSaveBOM_AutoLearnSkipsUnconfirmedInternalPN verifies that an
// internalPartNumber the system filled in but the operator never confirmed
// must NOT become a stored mapping. This is the core fail-safe behaviour: the
// system never promotes its own guesses into the cross-reference index.
func TestSaveBOM_AutoLearnSkipsUnconfirmedInternalPN(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{ID: "doc-uc-1", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                     "r1",
			LineNumber:             1,
			Description:            "Red wire",
			CustomerPartNumber:     "CBL-RED",
			InternalPartNumber:     "W-R-GUESS", // present but never confirmed by operator
			ManufacturerPartNumber: "MPN-GUESS",
			Quantity:               Quantity{Raw: "1", Flags: []string{}},
			Flags:                  []string{},
			ConfirmedFields:        []string{}, // no fields confirmed
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-uc-1/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-uc-1")
	w := httptest.NewRecorder()
	srv.saveBOM(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	all := srv.mappings.all("org-1")
	if len(all) != 0 {
		t.Errorf("expected 0 mappings (IPN unconfirmed), got %d", len(all))
	}
}

// TestSaveBOM_AutoLearnPersistsConfirmedInternalPN is the positive case:
// once the operator confirms the IPN cell, the mapping is auto-learned.
func TestSaveBOM_AutoLearnPersistsConfirmedInternalPN(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{ID: "doc-uc-2", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                     "r1",
			LineNumber:             1,
			Description:            "Red wire",
			CustomerPartNumber:     "CBL-RED",
			InternalPartNumber:     "W-R-035",
			ManufacturerPartNumber: "MPN-123",
			Quantity:               Quantity{Raw: "5", Flags: []string{}},
			Flags:                  []string{},
			ConfirmedFields:        []string{"internalPartNumber"},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-uc-2/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-uc-2")
	w := httptest.NewRecorder()
	srv.saveBOM(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	m, ok := srv.mappings.lookup("CBL-RED", "org-1")
	if !ok {
		t.Fatal("expected mapping after IPN confirmation")
	}
	if m.InternalPartNumber != "W-R-035" {
		t.Errorf("InternalPartNumber: want %q, got %q", "W-R-035", m.InternalPartNumber)
	}
}

// TestSaveBOM_AutoLearnSkipsRowsWithoutInternalPN verifies no mapping is
// created when internalPartNumber is empty.
func TestSaveBOM_AutoLearnSkipsRowsWithoutInternalPN(t *testing.T) {
	srv, token := newSettingsServer(t)
	doc := &Document{ID: "doc-al-4", Filename: "test.pdf", BOMRows: []BOMRow{}}
	srv.store.save(doc)

	rows := []BOMRow{
		{
			ID:                 "r1",
			LineNumber:         1,
			CustomerPartNumber: "CBL-RED",
			InternalPartNumber: "", // empty — no useful mapping yet
			Quantity:           Quantity{Raw: "1", Flags: []string{}},
			Flags:              []string{},
		},
	}

	body, _ := json.Marshal(rows)
	req := authedRequest(http.MethodPut, "/api/documents/doc-al-4/bom", string(body), token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "doc-al-4")
	_ = bytes.NewReader(body)
	w := httptest.NewRecorder()
	srv.saveBOM(w, req)

	all := srv.mappings.all("org-1")
	if len(all) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(all))
	}
}
