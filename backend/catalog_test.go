package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── scoreFingerprint ──────────────────────────────────────────────────────────

func TestScoreFingerprint_FullMatch(t *testing.T) {
	fp := PartFingerprint{Type: "wire", Diameter: "0.20mm", Standard: "bs4808", Color: "blue", Material: "pvc"}
	score, reasons := scoreFingerprint(fp, fp)
	assert.Equal(t, 1.0, score)
	assert.NotEmpty(t, reasons)
}

func TestScoreFingerprint_TypeMismatch_Fatal(t *testing.T) {
	a := PartFingerprint{Type: "wire", Diameter: "0.20mm"}
	b := PartFingerprint{Type: "connector", Diameter: "0.20mm"}
	score, _ := scoreFingerprint(a, b)
	assert.Equal(t, 0.0, score)
}

func TestScoreFingerprint_DiameterMismatch_Fatal(t *testing.T) {
	a := PartFingerprint{Type: "wire", Diameter: "0.20mm"}
	b := PartFingerprint{Type: "wire", Diameter: "0.35mm"}
	score, _ := scoreFingerprint(a, b)
	assert.Equal(t, 0.0, score)
}

func TestScoreFingerprint_ColorMismatch_NotFatal(t *testing.T) {
	a := PartFingerprint{Type: "wire", Diameter: "0.20mm", Color: "blue"}
	b := PartFingerprint{Type: "wire", Diameter: "0.20mm", Color: "red"}
	// type(0.30) + diameter(0.30) match out of total type(0.30)+diameter(0.30)+color(0.10) = 0.70
	score, _ := scoreFingerprint(a, b)
	assert.InDelta(t, 0.857, score, 0.001)
}

func TestScoreFingerprint_StandardMismatch_NotFatal(t *testing.T) {
	a := PartFingerprint{Type: "wire", Standard: "bs4808"}
	b := PartFingerprint{Type: "wire", Standard: "ul1015"}
	// type matches, standard doesn't; type(0.30)/total(0.30+0.25)=0.545
	score, _ := scoreFingerprint(a, b)
	assert.InDelta(t, 0.545, score, 0.001)
}

func TestScoreFingerprint_IgnoreMissingAttribute(t *testing.T) {
	// catalog has type+diameter only; row adds color — color not in catalog so not scored.
	catalog := PartFingerprint{Type: "wire", Diameter: "0.20mm"}
	row := PartFingerprint{Type: "wire", Diameter: "0.20mm", Color: "blue"}
	score, _ := scoreFingerprint(catalog, row)
	assert.Equal(t, 1.0, score)
}

func TestScoreFingerprint_BothEmpty(t *testing.T) {
	score, _ := scoreFingerprint(PartFingerprint{}, PartFingerprint{})
	assert.Equal(t, 0.0, score)
}

func TestScoreFingerprint_TypeOnly(t *testing.T) {
	a := PartFingerprint{Type: "wire"}
	b := PartFingerprint{Type: "wire"}
	// Only type is scored; both match → 1.0.
	score, _ := scoreFingerprint(a, b)
	assert.Equal(t, 1.0, score)
}

func TestScoreFingerprint_ReasonsListed(t *testing.T) {
	fp := PartFingerprint{Type: "wire", Diameter: "0.20mm", Color: "blue"}
	score, reasons := scoreFingerprint(fp, fp)
	assert.Equal(t, 1.0, score)
	assert.Contains(t, reasons, "type: wire")
	assert.Contains(t, reasons, "diameter: 0.20mm")
	assert.Contains(t, reasons, "color: blue")
}

// ── suggestFromCatalog ────────────────────────────────────────────────────────

func TestSuggestFromCatalog_ExactMPN(t *testing.T) {
	catalog := &fakeCatalogReader{
		byMPN: map[string]*CatalogPart{
			"MPN-123": {
				ID:                     "cat-1",
				InternalPartNumber:     "SC-001",
				ManufacturerPartNumber: "MPN-123",
				Fingerprint:            PartFingerprint{Type: "wire", Diameter: "0.20mm"},
			},
		},
	}
	row := &BOMRow{ManufacturerPartNumber: "MPN-123", Description: "Wire 0.2mm"}
	s, err := suggestFromCatalog(row, catalog)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, "SC-001", s.InternalPartNumber)
	assert.Equal(t, "exact_mpn", s.Source)
	assert.Equal(t, 1.0, s.Score)
}

func TestSuggestFromCatalog_FingerprintMatch(t *testing.T) {
	catalog := &fakeCatalogReader{
		byType: map[string][]*CatalogPart{
			"wire": {
				{
					ID:                 "cat-1",
					InternalPartNumber: "SC-001",
					Fingerprint:        PartFingerprint{Type: "wire", Diameter: "0.20mm", Standard: "bs4808", Color: "blue"},
				},
			},
		},
	}
	row := &BOMRow{Description: "Blue PVC BS4808 Wire 0.2mm"}
	s, err := suggestFromCatalog(row, catalog)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, "SC-001", s.InternalPartNumber)
	assert.Equal(t, "fingerprint", s.Source)
	assert.Greater(t, s.Score, 0.8)
}

func TestSuggestFromCatalog_NoMatch_WrongDiameter(t *testing.T) {
	catalog := &fakeCatalogReader{
		byType: map[string][]*CatalogPart{
			"wire": {
				{
					ID:                 "cat-1",
					InternalPartNumber: "SC-001",
					Fingerprint:        PartFingerprint{Type: "wire", Diameter: "0.35mm"},
				},
			},
		},
	}
	row := &BOMRow{Description: "Blue PVC Wire 0.20mm"}
	s, err := suggestFromCatalog(row, catalog)
	assert.NoError(t, err)
	assert.Nil(t, s) // diameter mismatch → score 0 → no suggestion
}

func TestSuggestFromCatalog_NilCatalog(t *testing.T) {
	row := &BOMRow{Description: "Wire 0.2mm"}
	s, err := suggestFromCatalog(row, nil)
	assert.NoError(t, err)
	assert.Nil(t, s)
}

func TestSuggestFromCatalog_NoTypeDetected_SkipsFingerprintSearch(t *testing.T) {
	catalog := &fakeCatalogReader{}
	row := &BOMRow{Description: "Some unrecognised component"}
	s, err := suggestFromCatalog(row, catalog)
	assert.NoError(t, err)
	assert.Nil(t, s)
	assert.False(t, catalog.byTypeCalled, "should not query by type when fingerprint has no type")
}

// ── fakeCatalogReader ─────────────────────────────────────────────────────────

type fakeCatalogReader struct {
	byMPN        map[string]*CatalogPart
	byType       map[string][]*CatalogPart
	byTypeCalled bool
}

func (f *fakeCatalogReader) findByMPN(mpn string) (*CatalogPart, bool, error) {
	if f.byMPN == nil {
		return nil, false, nil
	}
	p, ok := f.byMPN[mpn]
	return p, ok, nil
}

func (f *fakeCatalogReader) findByType(partType string) ([]*CatalogPart, error) {
	f.byTypeCalled = true
	if f.byType == nil {
		return nil, nil
	}
	return f.byType[partType], nil
}

func (f *fakeCatalogReader) incrementUsage(_ string) {}
