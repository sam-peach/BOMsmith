package main

import "encoding/json"

// mockAnalysis returns a realistic cable assembly BOM for development and testing.
// It exercises every flag type so the UI can be verified without a real drawing.
func mockAnalysis(ms mappingReader) AnalysisResult {
	rows := []llmRow{
		{
			// Clean row — no issues.
			RawLabel:               "1",
			Description:            "6-Way Housing, Molex MX-A 2.5mm pitch",
			RawQuantity:            "4",
			Unit:                   "EA",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "22-01-3067",
			SupplierReference:      "",
			Notes:                  "",
			Confidence:             0.97,
			Flags:                  []string{},
		},
		{
			// Supplier reference present (RS), no manufacturer PN yet.
			RawLabel:               "2",
			Description:            "Female Crimp Terminal, tin, 22–30 AWG",
			RawQuantity:            "24",
			Unit:                   "EA",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "",
			SupplierReference:      "679-4698",
			Notes:                  "",
			Confidence:             0.88,
			Flags:                  []string{},
		},
		{
			// Ambiguous quantity — inline unit "mm" conflicts with declared unit "M".
			RawLabel:               "HS2",
			Description:            "Heatshrink Sleeving, 3.2mm bore, 2:1 shrink ratio, black",
			RawQuantity:            "150mm",
			Unit:                   "M",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "",
			SupplierReference:      "512-3498",
			Notes:                  "Unit in drawing is mm; declared unit is M — confirm before ordering",
			Confidence:             0.65,
			Flags:                  []string{"needs-review"},
		},
		{
			// Cable length estimated from layout dimensions.
			RawLabel:               "Cable Type 14",
			Description:            "Cable, 6-core screened, 0.22mm² conductors, grey",
			RawQuantity:            "0.35m",
			Unit:                   "M",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "UNITRONIC-LiYCY-6x0.22",
			SupplierReference:      "122-6898",
			Notes:                  "Length derived from layout sheet dimensions",
			Confidence:             0.78,
			Flags:                  []string{"dimension-estimated"},
		},
		{
			// Customer part number present — mapping will be applied if one exists.
			RawLabel:               "5",
			Description:            "Cable Tie, 100mm, natural",
			RawQuantity:            "10",
			Unit:                   "EA",
			CustomerPartNumber:     "CUST-CT-01",
			ManufacturerPartNumber: "",
			SupplierReference:      "",
			Notes:                  "",
			Confidence:             0.91,
			Flags:                  []string{},
		},
		{
			// Missing manufacturer PN entirely, low confidence.
			RawLabel:               "HM1",
			Description:            "Printable Heatshrink Marker, 3mm bore",
			RawQuantity:            "0.3",
			Unit:                   "M",
			CustomerPartNumber:     "",
			ManufacturerPartNumber: "",
			SupplierReference:      "",
			Notes:                  "Manufacturer part number not found in drawing",
			Confidence:             0.55,
			Flags:                  []string{"needs-review"},
		},
	}

	// Serialise to JSON then run through parseBOMRows so all post-processing
	// (parseQuantity, detectSupplier, enrichFromSupplierRef, applyMapping) is applied
	// identically to the real pipeline.
	b, _ := json.Marshal(rows)
	result, warnings, _ := parseBOMRows(string(b), ms, nil)
	return AnalysisResult{BOMRows: result, Warnings: warnings}
}
