package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// parseQuantity
// ----------------------------------------------------------------------------

func TestParseQuantity_PlainInteger(t *testing.T) {
	q := parseQuantity("4", "EA")

	assert.Equal(t, "4", q.Raw)
	require.NotNil(t, q.Value)
	assert.Equal(t, 4.0, *q.Value)
	require.NotNil(t, q.Unit)
	assert.Equal(t, "EA", *q.Unit)
	assert.Empty(t, q.Flags)
}

func TestParseQuantity_PlainDecimal(t *testing.T) {
	q := parseQuantity("0.35", "M")

	require.NotNil(t, q.Value)
	assert.Equal(t, 0.35, *q.Value)
	assert.Empty(t, q.Flags)
}

func TestParseQuantity_InlineUnitMatchesDeclared(t *testing.T) {
	// "0.35m" with declared unit "M" — compatible, no flag.
	q := parseQuantity("0.35m", "M")

	require.NotNil(t, q.Value)
	assert.Equal(t, 0.35, *q.Value)
	assert.Empty(t, q.Flags, "compatible units should not be flagged")
}

func TestParseQuantity_InlineUnitConflictsDeclared(t *testing.T) {
	// Core bug case: "150mm" with declared unit "M" — these are NOT compatible.
	// Raw value must be preserved; unit_ambiguous must be set.
	q := parseQuantity("150mm", "M")

	assert.Equal(t, "150mm", q.Raw, "Raw must never be transformed")
	require.NotNil(t, q.Value)
	assert.Equal(t, 150.0, *q.Value, "Value should be 150, not 0.15")
	require.NotNil(t, q.Unit)
	assert.Equal(t, "MM", *q.Unit, "Unit should be the inline unit from the drawing")
	assert.Contains(t, q.Flags, "unit_ambiguous")
}

func TestParseQuantity_Unparseable(t *testing.T) {
	q := parseQuantity("approx 5", "EA")

	assert.Equal(t, "approx 5", q.Raw)
	assert.Nil(t, q.Value)
	assert.Contains(t, q.Flags, "unit_ambiguous")
}

func TestParseQuantity_EmptyRaw(t *testing.T) {
	q := parseQuantity("", "EA")

	assert.Equal(t, "", q.Raw)
	assert.Nil(t, q.Value)
	assert.Empty(t, q.Flags)
}

func TestParseQuantity_NormalizedEqualsValue(t *testing.T) {
	// Normalized should equal Value — we do not silently convert units.
	q := parseQuantity("3", "EA")

	require.NotNil(t, q.Value)
	require.NotNil(t, q.Normalized)
	assert.Equal(t, *q.Value, *q.Normalized)
}

// ----------------------------------------------------------------------------
// unitCompatible — expanded wiring-harness unit set
// ----------------------------------------------------------------------------

func TestUnitCompatible(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// existing
		{"M", "M", true},
		{"M", "METRES", true},
		{"METER", "M", true},
		{"MM", "M", false}, // millimetres ≠ metres — still a conflict
		{"EA", "EACH", true},
		{"EA", "M", false},
		{"MM", "MILLIMETRES", true},
		{"KG", "KILOGRAMS", true},
		{"KG", "EA", false},
		{"", "", false},
		// new wiring-harness units
		{"FT", "FEET", true},
		{"FT", "FOOT", true},
		{"FEET", "FOOT", true},
		{"IN", "INCH", true},
		{"IN", "INCHES", true},
		{"CM", "CENTIMETRE", true},
		{"CM", "CENTIMETERS", true},
		{"PR", "PAIR", true},
		{"PR", "PAIRS", true},
		{"SET", "SETS", true},
		{"LOT", "LOTS", true},
		{"MTR", "M", true},
		{"MTR", "METRES", true},
		{"PCS", "PC", true},
		{"PCS", "PIECE", true},
		{"PCS", "PIECES", true},
		{"FT", "M", false},  // different dimension families
		{"PR", "EA", false}, // pairs ≠ each
	}
	for _, tc := range tests {
		got := unitCompatible(tc.a, tc.b)
		assert.Equal(t, tc.want, got, "unitCompatible(%q, %q)", tc.a, tc.b)
	}
}

func TestParseQuantity_CanonicalUnitNormalization(t *testing.T) {
	// When inline unit is an alias, the stored unit should be the canonical form.
	tests := []struct {
		raw          string
		declared     string
		wantUnit     string
		wantAmbiguous bool
	}{
		{"3 EACH", "EA", "EA", false},   // EACH → EA
		{"5 METRES", "M", "M", false},   // METRES → M
		{"2 FEET", "FT", "FT", false},   // FEET → FT
		{"10 PCS", "EA", "EA", false},   // PCS → EA (compatible)
		{"4 PAIRS", "PR", "PR", false},  // PAIRS → PR
		{"1 SET", "SET", "SET", false},  // SET stays SET
		{"6 MTR", "M", "M", false},      // MTR → M
	}
	for _, tc := range tests {
		q := parseQuantity(tc.raw, tc.declared)
		require.NotNil(t, q.Unit, "unit should be set for %q", tc.raw)
		assert.Equal(t, tc.wantUnit, *q.Unit, "canonical unit for %q", tc.raw)
		if tc.wantAmbiguous {
			assert.Contains(t, q.Flags, "unit_ambiguous", "should flag ambiguous for %q", tc.raw)
		} else {
			assert.NotContains(t, q.Flags, "unit_ambiguous", "should not flag for %q", tc.raw)
		}
	}
}

func TestParseQuantity_WiringHarnessUnits(t *testing.T) {
	// Spot-check quantities that appear on real harness drawings.
	tests := []struct {
		raw       string
		declared  string
		wantValue float64
		wantUnit  string
	}{
		{"2PR", "PR", 2, "PR"},
		{"10FT", "FT", 10, "FT"},
		{"0.5M", "M", 0.5, "M"},
		{"1LOT", "LOT", 1, "LOT"},
		{"3SET", "SET", 3, "SET"},
	}
	for _, tc := range tests {
		q := parseQuantity(tc.raw, tc.declared)
		require.NotNil(t, q.Value, "value should parse for %q", tc.raw)
		assert.Equal(t, tc.wantValue, *q.Value, "value for %q", tc.raw)
		require.NotNil(t, q.Unit, "unit should be set for %q", tc.raw)
		assert.Equal(t, tc.wantUnit, *q.Unit, "unit for %q", tc.raw)
		assert.NotContains(t, q.Flags, "unit_ambiguous", "should not flag for %q", tc.raw)
	}
}

// ----------------------------------------------------------------------------
// detectSupplier
// ----------------------------------------------------------------------------

func TestDetectSupplier_RS(t *testing.T) {
	row := &BOMRow{SupplierReference: "679-4698", ManufacturerPartNumber: "some-mpn"}
	detectSupplier(row)

	assert.Equal(t, "RS", row.Supplier)
	assert.Contains(t, row.Flags, "supplier_reference_detected")
}

func TestDetectSupplier_RSSevenDigit(t *testing.T) {
	row := &BOMRow{SupplierReference: "1234567", ManufacturerPartNumber: "some-mpn"}
	detectSupplier(row)

	assert.Equal(t, "RS", row.Supplier)
}

func TestDetectSupplier_Farnell(t *testing.T) {
	row := &BOMRow{SupplierReference: "1234567A", ManufacturerPartNumber: "some-mpn"}
	detectSupplier(row)

	assert.Equal(t, "Farnell", row.Supplier)
}

func TestDetectSupplier_Unknown(t *testing.T) {
	row := &BOMRow{SupplierReference: "DIGIKEY-ABC123", ManufacturerPartNumber: "some-mpn"}
	detectSupplier(row)

	assert.Equal(t, "Unknown", row.Supplier)
	assert.Contains(t, row.Flags, "supplier_reference_detected")
}

func TestDetectSupplier_NoRef(t *testing.T) {
	row := &BOMRow{SupplierReference: ""}
	detectSupplier(row)

	assert.Equal(t, "", row.Supplier)
	assert.NotContains(t, row.Flags, "supplier_reference_detected")
}

func TestDetectSupplier_MissingMPN_AddsNote(t *testing.T) {
	row := &BOMRow{SupplierReference: "679-4698", ManufacturerPartNumber: ""}
	detectSupplier(row)

	assert.Contains(t, row.Notes, "Supplier reference detected")
}

// ----------------------------------------------------------------------------
// enrichFromSupplierRef
// ----------------------------------------------------------------------------

func TestEnrichFromSupplierRef_AddsMockMPN(t *testing.T) {
	row := &BOMRow{SupplierReference: "679-4698", ManufacturerPartNumber: "", Confidence: 0.9}
	enrichFromSupplierRef(row)

	assert.NotEmpty(t, row.ManufacturerPartNumber)
	assert.Contains(t, row.Notes, "verify before use")
	assert.Contains(t, row.Flags, "low_confidence")
	assert.LessOrEqual(t, row.Confidence, 0.6)
}

func TestEnrichFromSupplierRef_SkipsIfMPNExists(t *testing.T) {
	row := &BOMRow{SupplierReference: "679-4698", ManufacturerPartNumber: "22-01-3067"}
	enrichFromSupplierRef(row)

	assert.Equal(t, "22-01-3067", row.ManufacturerPartNumber, "existing MPN must not be overwritten")
}

func TestEnrichFromSupplierRef_SkipsIfNoRef(t *testing.T) {
	row := &BOMRow{SupplierReference: "", ManufacturerPartNumber: ""}
	enrichFromSupplierRef(row)

	assert.Empty(t, row.ManufacturerPartNumber)
}

// ----------------------------------------------------------------------------
// appendFlag / appendNote
// ----------------------------------------------------------------------------

func TestAppendFlag_NoDuplicates(t *testing.T) {
	flags := []string{"a", "b"}
	flags = appendFlag(flags, "b")
	assert.Equal(t, []string{"a", "b"}, flags)
}

func TestAppendFlag_AddsNew(t *testing.T) {
	flags := appendFlag([]string{"a"}, "b")
	assert.Equal(t, []string{"a", "b"}, flags)
}

func TestAppendNote_Empty(t *testing.T) {
	assert.Equal(t, "new note", appendNote("", "new note"))
}

func TestAppendNote_Append(t *testing.T) {
	assert.Equal(t, "first; second", appendNote("first", "second"))
}

// ----------------------------------------------------------------------------
// applyMapping
// ----------------------------------------------------------------------------

func TestApplyMapping_FillsFields(t *testing.T) {
	ms := newTestMappingReader()
	ms.add(&Mapping{
		CustomerPartNumber:     "CUST-CT-01",
		InternalPartNumber:     "SC-001",
		ManufacturerPartNumber: "MPN-001",
	})

	row := &BOMRow{CustomerPartNumber: "CUST-CT-01"}
	applyMapping(row, ms)

	assert.Equal(t, "SC-001", row.InternalPartNumber)
	assert.Equal(t, "MPN-001", row.ManufacturerPartNumber)
	assert.Contains(t, row.Flags, "mapping_applied")
	assert.Contains(t, row.Notes, "Matched from previous mapping")
}

func TestApplyMapping_CaseInsensitive(t *testing.T) {
	ms := newTestMappingReader()
	ms.add(&Mapping{CustomerPartNumber: "cust-ct-01", InternalPartNumber: "SC-001"})

	row := &BOMRow{CustomerPartNumber: "CUST-CT-01"}
	applyMapping(row, ms)

	assert.Equal(t, "SC-001", row.InternalPartNumber)
}

func TestApplyMapping_NoMatch(t *testing.T) {
	ms := newTestMappingReader()
	row := &BOMRow{CustomerPartNumber: "UNKNOWN"}
	applyMapping(row, ms)

	assert.Empty(t, row.InternalPartNumber)
	assert.NotContains(t, row.Flags, "mapping_applied")
}

func TestApplyMapping_DoesNotOverwriteExisting(t *testing.T) {
	ms := newTestMappingReader()
	ms.add(&Mapping{CustomerPartNumber: "CUST-CT-01", InternalPartNumber: "SC-001"})

	row := &BOMRow{
		CustomerPartNumber: "CUST-CT-01",
		InternalPartNumber: "SC-ALREADY-SET",
	}
	applyMapping(row, ms)

	assert.Equal(t, "SC-ALREADY-SET", row.InternalPartNumber, "existing internal PN must not be overwritten")
}

// ----------------------------------------------------------------------------
// normaliseToMetres
// ----------------------------------------------------------------------------

func TestNormaliseToMetres_MM(t *testing.T) {
	val := 660.0
	unit := "MM"
	q := Quantity{Raw: "660mm", Value: &val, Unit: &unit, Flags: []string{}}
	normaliseToMetres(&q)

	assert.Equal(t, "660mm", q.Raw, "Raw must not be modified")
	require.NotNil(t, q.Value)
	assert.InDelta(t, 0.66, *q.Value, 1e-9)
	require.NotNil(t, q.Unit)
	assert.Equal(t, "M", *q.Unit)
	assert.Empty(t, q.Flags)
}

func TestNormaliseToMetres_CM(t *testing.T) {
	val := 150.0
	unit := "CM"
	q := Quantity{Raw: "150cm", Value: &val, Unit: &unit, Flags: []string{}}
	normaliseToMetres(&q)

	assert.Equal(t, "150cm", q.Raw)
	require.NotNil(t, q.Value)
	assert.InDelta(t, 1.5, *q.Value, 1e-9)
	require.NotNil(t, q.Unit)
	assert.Equal(t, "M", *q.Unit)
}

func TestNormaliseToMetres_M_Unchanged(t *testing.T) {
	val := 2.0
	unit := "M"
	q := Quantity{Raw: "2M", Value: &val, Unit: &unit, Flags: []string{}}
	normaliseToMetres(&q)

	require.NotNil(t, q.Value)
	assert.Equal(t, 2.0, *q.Value, "M values must not be changed")
	assert.Equal(t, "M", *q.Unit)
}

func TestNormaliseToMetres_EA_Unchanged(t *testing.T) {
	val := 4.0
	unit := "EA"
	q := Quantity{Raw: "4", Value: &val, Unit: &unit, Flags: []string{}}
	normaliseToMetres(&q)

	require.NotNil(t, q.Value)
	assert.Equal(t, 4.0, *q.Value, "non-length units must not be changed")
	assert.Equal(t, "EA", *q.Unit)
}

func TestNormaliseToMetres_NilValue_Unchanged(t *testing.T) {
	unit := "MM"
	q := Quantity{Raw: "approx 5", Value: nil, Unit: &unit, Flags: []string{"unit_ambiguous"}}
	normaliseToMetres(&q)

	assert.Nil(t, q.Value, "nil Value must not be changed")
	assert.Equal(t, "MM", *q.Unit, "unit must not be changed when value is nil")
}

// ----------------------------------------------------------------------------
// applyMapping — MPN fallback
// ----------------------------------------------------------------------------

func TestApplyMapping_FallsBackToMPNWhenNoCPN(t *testing.T) {
	ms := newTestMappingReader()
	ms.add(&Mapping{
		CustomerPartNumber: "43031-0007",
		InternalPartNumber: "W-INTERNAL",
	})

	row := &BOMRow{
		CustomerPartNumber:     "",
		ManufacturerPartNumber: "43031-0007",
	}
	applyMapping(row, ms)

	assert.Equal(t, "W-INTERNAL", row.InternalPartNumber)
	assert.Contains(t, row.Flags, "mapping_applied")
}

func TestApplyMapping_SkipsWhenBothCPNAndMPNEmpty(t *testing.T) {
	ms := newTestMappingReader()
	row := &BOMRow{CustomerPartNumber: "", ManufacturerPartNumber: ""}
	applyMapping(row, ms)

	assert.Empty(t, row.InternalPartNumber)
	assert.NotContains(t, row.Flags, "mapping_applied")
}

// ----------------------------------------------------------------------------
// parseBOMRows — truncation recovery
// ----------------------------------------------------------------------------

func TestParseBOMRows_TruncatedJSON_RecoverCompleteRows(t *testing.T) {
	// Simulate a response that was cut off mid-way through the second object.
	truncated := `[
  {"rawLabel":"Item 1","description":"Red wire","rawQuantity":"2","unit":"M","customerPartNumber":"","manufacturerPartNumber":"MPN-1","supplierReference":"","notes":"","confidence":0.9,"flags":[]},
  {"rawLabel":"Item 2","description":"Connector","rawQuantity":"1","unit":"EA","customerPartNumber":"","manufacturerPartNumber":"MPN-2","supplierReference":"","notes":"","confide`

	rows, warnings, err := parseBOMRows(truncated, nil, nil)

	require.NoError(t, err, "truncated response should not return an error")
	require.Len(t, rows, 1, "should recover the one complete row")
	assert.Equal(t, "Red wire", rows[0].Description)
	assert.True(t, len(warnings) > 0, "should warn about truncation")
	assert.Contains(t, warnings[0], "truncated")
}

func TestParseBOMRows_TruncatedJSON_NoCompleteRows(t *testing.T) {
	// Response cut off before any complete object.
	truncated := `[{"rawLabel":"Item 1","description":"Cut off mid`

	_, _, err := parseBOMRows(truncated, nil, nil)

	assert.Error(t, err, "no recoverable rows should still error")
}

func TestParseBOMRows_ValidJSON_UnaffectedByRecoveryLogic(t *testing.T) {
	valid := `[{"rawLabel":"W1","description":"Black wire","rawQuantity":"3","unit":"M","customerPartNumber":"","manufacturerPartNumber":"MPN-3","supplierReference":"","notes":"","confidence":0.95,"flags":[]}]`

	rows, warnings, err := parseBOMRows(valid, nil, nil)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, warnings)
}

// ----------------------------------------------------------------------------
// Reference-entry filtering
// ----------------------------------------------------------------------------

func TestParseBOMRows_ReferenceEntry_IsFiltered(t *testing.T) {
	// A type-reference row (reference_entry: true) must not appear in the output.
	input := `[
		{"rawLabel":"1","description":"Red wire","rawQuantity":"0.5","unit":"M","customerPartNumber":"","manufacturerPartNumber":"W-RED","supplierReference":"","notes":"","confidence":0.9,"flags":[]},
		{"rawLabel":"HS1","description":"Polyolefin Heatshrink 3:1 3mm bore","rawQuantity":"","unit":"","customerPartNumber":"","manufacturerPartNumber":"","supplierReference":"","notes":"","confidence":0.9,"flags":[],"reference_entry":true}
	]`

	rows, _, err := parseBOMRows(input, nil, nil)

	require.NoError(t, err)
	require.Len(t, rows, 1, "reference_entry rows must be filtered out")
	assert.Equal(t, "Red wire", rows[0].Description)
}

func TestParseBOMRows_ReferenceEntry_AllFiltered_ReturnsNoRowsWarning(t *testing.T) {
	// If all rows are reference entries the result should be empty with the usual warning.
	input := `[
		{"rawLabel":"HS1","description":"Polyolefin Heatshrink","rawQuantity":"","unit":"","customerPartNumber":"","manufacturerPartNumber":"","supplierReference":"","notes":"","confidence":0.9,"flags":[],"reference_entry":true},
		{"rawLabel":"SL1","description":"Expandable Sleeve","rawQuantity":"","unit":"","customerPartNumber":"","manufacturerPartNumber":"","supplierReference":"","notes":"","confidence":0.9,"flags":[],"reference_entry":true}
	]`

	rows, warnings, err := parseBOMRows(input, nil, nil)

	require.NoError(t, err)
	assert.Empty(t, rows)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "No BOM items")
}

func TestParseBOMRows_ReferenceEntryFalse_IsIncluded(t *testing.T) {
	// reference_entry: false (explicit) should still be included.
	input := `[{"rawLabel":"1","description":"Blue wire","rawQuantity":"1","unit":"M","customerPartNumber":"","manufacturerPartNumber":"W-BLUE","supplierReference":"","notes":"","confidence":0.9,"flags":[],"reference_entry":false}]`

	rows, _, err := parseBOMRows(input, nil, nil)

	require.NoError(t, err)
	require.Len(t, rows, 1)
}
