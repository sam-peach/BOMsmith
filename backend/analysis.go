package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	anthropicAPIURL  = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	anthropicModel   = "claude-sonnet-4-6"
)

var anthropicClient = &http.Client{Timeout: 5 * time.Minute}

// analyzeDocument is the pipeline entry point.
// When apiKey is empty it returns mock data for development/testing.
func analyzeDocument(doc *Document, apiKey string, ms mappingReader) (AnalysisResult, error) {
	if apiKey == "" {
		return mockAnalysis(ms), nil
	}

	text, err := extractText(doc.FilePath)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("PDF text extraction: %w", err)
	}
	if text == "" {
		return AnalysisResult{}, fmt.Errorf(
			"this PDF contains no selectable text — it may be a scanned drawing; " +
				"a text-based PDF is required for automatic extraction",
		)
	}

	return interpretText(text, apiKey, ms)
}

// interpretText sends the extracted drawing text to the Anthropic API,
// parses the response, then post-processes each row.
func interpretText(text, apiKey string, ms mappingReader) (AnalysisResult, error) {
	raw, err := callAnthropic(text, apiKey)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("Anthropic API: %w", err)
	}

	rows, warnings, err := parseBOMRows(raw, ms)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("parsing LLM response: %w", err)
	}

	return AnalysisResult{BOMRows: rows, Warnings: warnings}, nil
}

// callAnthropic sends the drawing text to the Claude API and returns the raw text response.
func callAnthropic(drawingText, apiKey string) (string, error) {
	system := `You are a BOM extraction assistant for a wiring harness manufacturer.

These drawings follow a standard multi-sheet format:
  Sheet 1  Schematic — wire routing, connector labels (SK1, SK23…), terminal symbols, heatshrink call-outs
  Sheet 2  Physical layout — harness dimensions in mm for each wire run
  Sheet 3  Part Reference table, Cable Type Reference table, Heatshrink & Cable Sleeve Type Reference table

WHAT TO EXTRACT AND HOW:

1. PART REFERENCE TABLE (primary source)
   Columns: Item No. | Quantity | Description | Manufacturer's Part No. | Supplier's Part No. | Comments
   Extract every numbered item as one BOM row, in table order.
   The manufacturerPartNumber is the part number string only — strip any leading manufacturer name.
   The Supplier's Part No. column may contain RS or Farnell references — capture these in supplierReference.

2. CABLES — one row per type × colour combination
   Quantity in metres, derived from physical layout on sheet 2.
   rawQuantity should reflect the drawing value (e.g. "0.35m").

3. HEATSHRINK AND CABLE MARKERS
   HS1, HS2… — plain heatshrink sleeving, quantity in metres.
   HM1, HM2… — heatshrink cable markers, quantity in metres (per-marker length × count).

OUTPUT FORMAT
Single valid JSON array. No markdown. No code fences. Begin with [.

Each element must have exactly these fields:
  rawLabel               (string) — label as it appears on the drawing
  description            (string) — clear engineering description including key spec
  rawQuantity            (string) — quantity EXACTLY as written on the drawing; NEVER transform
  unit                   (string) — canonical unit: EA for each, M for metres
  customerPartNumber     (string) — "" for wiring harness drawings
  manufacturerPartNumber (string) — from Part Reference table; "" if absent
  supplierReference      (string) — RS or Farnell distributor code if present; "" otherwise
  notes                  (string) — concise; "" if nothing to flag
  confidence             (number) — 0.0–1.0
  flags                  (array)  — subset of: needs-review, low-confidence, ambiguous-spec,
                                    dimension-estimated, missing-manufacturer-pn

RULES
- Do not invent part numbers or quantities
- Set confidence < 0.70 and add needs-review for anything ambiguous
- If no items are identifiable, return []`

	reqBody := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model:     anthropicModel,
		MaxTokens: 8192,
		System:    system,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "user", Content: "Drawing text:\n\n" + drawingText},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, anthropicAPIURL, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := anthropicClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var ar struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &ar); err != nil {
		return "", fmt.Errorf("parsing API response: %w", err)
	}
	if ar.Error != nil {
		return "", fmt.Errorf("%s", ar.Error.Message)
	}
	if len(ar.Content) == 0 || ar.Content[0].Text == "" {
		return "", fmt.Errorf("empty response from API")
	}

	return ar.Content[0].Text, nil
}

// llmRow is the JSON shape returned by the LLM.
type llmRow struct {
	RawLabel               string   `json:"rawLabel"`
	Description            string   `json:"description"`
	RawQuantity            string   `json:"rawQuantity"`
	Unit                   string   `json:"unit"` // canonical unit declared by LLM
	CustomerPartNumber     string   `json:"customerPartNumber"`
	ManufacturerPartNumber string   `json:"manufacturerPartNumber"`
	SupplierReference      string   `json:"supplierReference"`
	Notes                  string   `json:"notes"`
	Confidence             float64  `json:"confidence"`
	Flags                  []string `json:"flags"`
}

// parseBOMRows converts the raw LLM text into BOMRows and runs post-processing.
func parseBOMRows(text string, ms mappingReader) ([]BOMRow, []string, error) {
	text = strings.TrimSpace(text)

	// Strip markdown fences.
	if after, found := strings.CutPrefix(text, "```json"); found {
		text = after
	} else if after, found := strings.CutPrefix(text, "```"); found {
		text = after
	}
	if i := strings.LastIndex(text, "```"); i != -1 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)

	if !strings.HasPrefix(text, "[") {
		start := strings.Index(text, "[")
		if start == -1 {
			return nil, nil, fmt.Errorf("no JSON array in response: %.300s", text)
		}
		text = text[start:]
	}
	if end := strings.LastIndex(text, "]"); end != -1 {
		text = text[:end+1]
	}

	var raw []llmRow
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, nil, fmt.Errorf("JSON unmarshal: %w — response: %.300s", err, text)
	}

	rows := make([]BOMRow, 0, len(raw))
	for i, r := range raw {
		if r.Flags == nil {
			r.Flags = []string{}
		}
		r.Confidence = clamp01(r.Confidence)

		row := BOMRow{
			ID:                     fmt.Sprintf("row-%d", i+1),
			LineNumber:             i + 1,
			RawLabel:               r.RawLabel,
			Description:            r.Description,
			Quantity:               parseQuantity(r.RawQuantity, r.Unit),
			CustomerPartNumber:     r.CustomerPartNumber,
			InternalPartNumber:     "",
			ManufacturerPartNumber: r.ManufacturerPartNumber,
			SupplierReference:      r.SupplierReference,
			Notes:                  r.Notes,
			Confidence:             r.Confidence,
			Flags:                  r.Flags,
		}

		detectSupplier(&row)
		enrichFromSupplierRef(&row)
		applyMapping(&row, ms)

		if row.ManufacturerPartNumber == "" {
			row.Flags = appendFlag(row.Flags, "missing_part_number")
		}
		// Promote quantity-level flags up to row level so the frontend can tint the row.
		for _, f := range row.Quantity.Flags {
			row.Flags = appendFlag(row.Flags, f)
		}

		rows = append(rows, row)
	}

	warnings := []string{}
	if len(rows) == 0 {
		warnings = append(warnings, "No BOM items were identified in this drawing.")
	}
	return rows, warnings, nil
}

// quantityRE matches: optional number (int or decimal) followed by optional unit letters.
var quantityRE = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*([a-z]+)?\s*$`)

// parseQuantity parses a raw quantity string and the canonical unit declared by the LLM
// into a Quantity struct. It never silently transforms values.
func parseQuantity(rawStr, declaredUnit string) Quantity {
	q := Quantity{Raw: rawStr, Flags: []string{}}

	if strings.TrimSpace(rawStr) == "" {
		return q
	}

	m := quantityRE.FindStringSubmatch(rawStr)
	if m == nil {
		q.Flags = append(q.Flags, "unit_ambiguous")
		return q
	}

	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		q.Flags = append(q.Flags, "unit_ambiguous")
		return q
	}
	q.Value = &val

	inlineUnit := strings.ToUpper(strings.TrimSpace(m[2]))
	canonical := strings.ToUpper(strings.TrimSpace(declaredUnit))

	switch {
	case inlineUnit != "" && canonical != "" && !unitCompatible(inlineUnit, canonical):
		// The drawing wrote e.g. "150mm" but the LLM declared unit "M" — conflict.
		q.Flags = append(q.Flags, "unit_ambiguous")
		u := inlineUnit
		q.Unit = &u
	case inlineUnit != "":
		u := inlineUnit
		q.Unit = &u
	case canonical != "":
		u := canonical
		q.Unit = &u
	}

	// Normalized: same as Value for now — we do not silently transform units.
	q.Normalized = q.Value

	return q
}

// unitCompatible returns true when the two unit strings refer to the same unit.
func unitCompatible(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	canon := map[string]string{
		"M": "M", "METRES": "M", "METER": "M", "METERS": "M",
		"MM": "MM", "MILLIMETRES": "MM", "MILLIMETERS": "MM",
		"EA": "EA", "EACH": "EA", "PCS": "EA", "PC": "EA",
		"KG": "KG", "KILOGRAMS": "KG",
	}
	ca, cb := canon[a], canon[b]
	return ca != "" && ca == cb
}

// rsRE matches RS Components part numbers: NNN-NNNN or plain 7-digit.
var rsRE = regexp.MustCompile(`(?i)^(rs\s*)?(\d{3}-\d{4}|\d{7})$`)

// farnellRE matches Farnell order codes: 7-digit optionally followed by one letter.
var farnellRE = regexp.MustCompile(`(?i)^(farnell\s*)?(\d{7}[a-z]?)$`)

// detectSupplier classifies the SupplierReference field and sets the Supplier name.
func detectSupplier(row *BOMRow) {
	ref := strings.TrimSpace(row.SupplierReference)
	if ref == "" {
		return
	}

	row.Flags = appendFlag(row.Flags, "supplier_reference_detected")

	switch {
	case rsRE.MatchString(ref):
		row.Supplier = "RS"
	case farnellRE.MatchString(ref):
		row.Supplier = "Farnell"
	default:
		row.Supplier = "Unknown"
	}

	if row.ManufacturerPartNumber == "" {
		row.Notes = appendNote(row.Notes, "Supplier reference detected — verify manufacturer part")
	}
}

// enrichFromSupplierRef adds a placeholder MPN when only a supplier reference is available.
// Structured so a real API lookup can replace this body later.
func enrichFromSupplierRef(row *BOMRow) {
	if row.SupplierReference == "" || row.ManufacturerPartNumber != "" {
		return
	}
	// TODO: replace with real supplier API lookup.
	row.ManufacturerPartNumber = "MPN-" + strings.ToUpper(row.SupplierReference)
	row.Notes = appendNote(row.Notes, "Manufacturer P/N derived from supplier reference — verify before use")
	row.Flags = appendFlag(row.Flags, "low_confidence")
	if row.Confidence > 0.6 {
		row.Confidence = 0.6
	}
}

// applyMapping checks for a known mapping and fills in InternalPartNumber /
// ManufacturerPartNumber from the stored record.
func applyMapping(row *BOMRow, ms mappingReader) {
	if ms == nil || row.CustomerPartNumber == "" {
		return
	}
	m, ok := ms.lookup(row.CustomerPartNumber)
	if !ok {
		return
	}

	if row.InternalPartNumber == "" && m.InternalPartNumber != "" {
		row.InternalPartNumber = m.InternalPartNumber
	}
	if row.ManufacturerPartNumber == "" && m.ManufacturerPartNumber != "" {
		row.ManufacturerPartNumber = m.ManufacturerPartNumber
	}

	row.Flags = appendFlag(row.Flags, "mapping_applied")
	row.Notes = appendNote(row.Notes, "Matched from previous mapping")

	go ms.touchLastUsed(row.CustomerPartNumber) // fire-and-forget; non-critical
}

// appendFlag adds f to flags only if not already present.
func appendFlag(flags []string, f string) []string {
	for _, existing := range flags {
		if existing == f {
			return flags
		}
	}
	return append(flags, f)
}

// appendNote appends note to existing, separated by "; ".
func appendNote(existing, note string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return note
	}
	return existing + "; " + note
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
