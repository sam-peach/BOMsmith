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
	anthropicVersion = "2023-06-01"
	anthropicModel   = "claude-sonnet-4-6"
	anthropicMaxTokens = 32000
)

// anthropicAPIURL is a var so tests can swap it for a local httptest server.
var anthropicAPIURL = "https://api.anthropic.com/v1/messages"

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

// callAnthropic sends the drawing text to the Claude API using tool use to
// guarantee structured JSON output. The model is forced to call extract_bom,
// so it can never return prose instead of a BOM array.
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

TYPE REFERENCE TABLES — do NOT emit as BOM rows
   Sheet 3 may contain "Heatshrink & Cable Sleeve Type Reference" or similar tables
   whose columns are Type No. | Specification | Approvals | Comments.
   These are specification catalogues, not BOM items — they list what HS1/SL2/HM3 etc.
   mean, not how many are needed. Set reference_entry to true for any row sourced from
   such a table. The backend will filter them out before presenting results to the user.

RULES
- Do not invent part numbers or quantities
- Set confidence < 0.70 and add needs-review for anything ambiguous
- If no items are identifiable, call extract_bom with an empty rows array`

	// Tool schema — every field matches llmRow so the decoded input can be
	// marshalled back to JSON and fed directly into parseBOMRows.
	tool := map[string]any{
		"name":        "extract_bom",
		"description": "Return the complete Bill of Materials extracted from the drawing.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rows": map[string]any{
					"type":        "array",
					"description": "One element per BOM line item. Empty array if no items found.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"rawLabel":               map[string]any{"type": "string", "description": "Label as it appears on the drawing"},
							"description":            map[string]any{"type": "string", "description": "Clear engineering description including key spec"},
							"rawQuantity":            map[string]any{"type": "string", "description": "Quantity EXACTLY as written on the drawing; never transform"},
							"unit":                   map[string]any{"type": "string", "description": "Canonical unit: EA for each, M for metres"},
							"customerPartNumber":     map[string]any{"type": "string", "description": "Empty string for wiring harness drawings"},
							"manufacturerPartNumber": map[string]any{"type": "string", "description": "From Part Reference table; empty string if absent"},
							"supplierReference":      map[string]any{"type": "string", "description": "RS or Farnell distributor code if present; empty string otherwise"},
							"notes":                  map[string]any{"type": "string", "description": "Concise notes; empty string if nothing to flag"},
							"confidence":             map[string]any{"type": "number", "description": "0.0–1.0"},
							"flags": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "string"},
								"description": "Subset of: needs-review, low-confidence, ambiguous-spec, dimension-estimated, missing-manufacturer-pn",
							},
							"reference_entry": map[string]any{"type": "boolean", "description": "true if this row comes from a type reference table"},
						},
						"required": []string{
							"rawLabel", "description", "rawQuantity", "unit",
							"customerPartNumber", "manufacturerPartNumber", "supplierReference",
							"notes", "confidence", "flags",
						},
					},
				},
			},
			"required": []string{"rows"},
		},
	}

	reqBody := map[string]any{
		"model":      anthropicModel,
		"max_tokens": anthropicMaxTokens,
		"system":     system,
		"tools":      []any{tool},
		// Force the model to call extract_bom — prose output is impossible.
		"tool_choice": map[string]string{"type": "tool", "name": "extract_bom"},
		"messages": []map[string]string{
			{"role": "user", "content": "Drawing text:\n\n" + drawingText},
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

	// Parse tool-use response: content blocks have type "tool_use" with an
	// "input" field containing the already-structured arguments.
	var ar struct {
		Content []struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
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

	for _, block := range ar.Content {
		if block.Type == "tool_use" && block.Input != nil {
			// block.Input is {"rows": [...]}; extract the array and return as a
			// JSON string so parseBOMRows can process it unchanged.
			var wrapper struct {
				Rows json.RawMessage `json:"rows"`
			}
			if err := json.Unmarshal(block.Input, &wrapper); err != nil {
				return "", fmt.Errorf("decoding tool input: %w", err)
			}
			if wrapper.Rows == nil {
				return "[]", nil
			}
			return string(wrapper.Rows), nil
		}
	}

	return "", fmt.Errorf("no tool_use block in API response")
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
	ReferenceEntry         bool     `json:"reference_entry,omitempty"` // true for type-reference table rows
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
	var raw []llmRow
	warnings := []string{}
	// Use a decoder so that any trailing text after the ] (e.g. LLM commentary)
	// is ignored without needing to pre-strip it. This also preserves the full
	// text for truncation recovery — stripping on LastIndex("]") breaks recovery
	// because the last ] may be inside a "flags":[] value, not the array close.
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&raw); err != nil {
		// The LLM response may have been cut off at the token limit.
		// Attempt to recover whatever complete objects were received.
		recovered, ok := recoverTruncatedArray(text)
		if !ok {
			return nil, nil, fmt.Errorf("JSON unmarshal: %w — response: %.300s", err, text)
		}
		if err2 := json.Unmarshal([]byte(recovered), &raw); err2 != nil {
			return nil, nil, fmt.Errorf("JSON unmarshal: %w — response: %.300s", err, text)
		}
		warnings = append(warnings, "The drawing response was truncated — the BOM may be incomplete. Try re-analysing, or split the drawing into smaller sections.")
	}

	rows := make([]BOMRow, 0, len(raw))
	for i, r := range raw {
		if r.ReferenceEntry {
			continue // type-reference table rows are spec catalogues, not BOM items
		}
		if r.Flags == nil {
			r.Flags = []string{}
		}
		r.Confidence = clamp01(r.Confidence)

		qty := parseQuantity(r.RawQuantity, r.Unit)
		normaliseToMetres(&qty)
		row := BOMRow{
			ID:                     fmt.Sprintf("row-%d", i+1),
			LineNumber:             i + 1,
			RawLabel:               r.RawLabel,
			Description:            r.Description,
			Quantity:               qty,
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

	if len(rows) == 0 {
		warnings = append(warnings, "No BOM items were identified in this drawing.")
	}
	return rows, warnings, nil
}

// recoverTruncatedArray attempts to salvage a JSON array that was cut off
// before the closing ]. It finds the last complete object (ending with })
// and closes the array. Returns the repaired text and true on success.
func recoverTruncatedArray(text string) (string, bool) {
	idx := strings.LastIndex(text, "}")
	if idx < 0 {
		return "", false
	}
	candidate := strings.TrimSpace(text[:idx+1]) + "]"
	// Verify it at least opens with [.
	if !strings.HasPrefix(strings.TrimSpace(candidate), "[") {
		return "", false
	}
	return candidate, true
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

	inlineUnit := canonicalUnit(strings.ToUpper(strings.TrimSpace(m[2])))
	canonical := canonicalUnit(strings.ToUpper(strings.TrimSpace(declaredUnit)))

	switch {
	case inlineUnit != "" && canonical != "" && !unitCompatible(inlineUnit, canonical):
		// The drawing wrote e.g. "150mm" but the LLM declared unit "M" — conflict.
		q.Flags = append(q.Flags, "unit_ambiguous")
		q.Unit = &inlineUnit
	case inlineUnit != "":
		q.Unit = &inlineUnit
	case canonical != "":
		q.Unit = &canonical
	}

	// Normalized: same as Value for now — we do not silently transform units.
	q.Normalized = q.Value

	return q
}

// normaliseToMetres converts MM or CM quantities to M in-place.
// Raw is never modified. Value and Unit are updated only when both are set.
func normaliseToMetres(q *Quantity) {
	if q.Value == nil || q.Unit == nil {
		return
	}
	switch *q.Unit {
	case "MM":
		v := *q.Value / 1000
		q.Value = &v
		m := "M"
		q.Unit = &m
	case "CM":
		v := *q.Value / 100
		q.Value = &v
		m := "M"
		q.Unit = &m
	}
}

// unitAliases maps every known unit alias to its canonical form.
// Canonical forms are the shortest, most-recognised abbreviation for the unit.
var unitAliases = map[string]string{
	// metres
	"M": "M", "METRES": "M", "METER": "M", "METERS": "M", "MTR": "M",
	// millimetres
	"MM": "MM", "MILLIMETRES": "MM", "MILLIMETERS": "MM",
	// centimetres
	"CM": "CM", "CENTIMETRE": "CM", "CENTIMETRES": "CM", "CENTIMETER": "CM", "CENTIMETERS": "CM",
	// feet
	"FT": "FT", "FEET": "FT", "FOOT": "FT",
	// inches
	"IN": "IN", "INCH": "IN", "INCHES": "IN",
	// each / piece
	"EA": "EA", "EACH": "EA", "PCS": "EA", "PC": "EA", "PIECE": "EA", "PIECES": "EA",
	// pair
	"PR": "PR", "PAIR": "PR", "PAIRS": "PR",
	// set
	"SET": "SET", "SETS": "SET",
	// lot
	"LOT": "LOT", "LOTS": "LOT",
	// mass
	"KG": "KG", "KILOGRAMS": "KG", "KILOGRAM": "KG",
	"G": "G", "GRAMS": "G", "GRAM": "G",
}

// canonicalUnit returns the canonical unit string for a given alias,
// or the input unchanged if it is not in the alias table.
func canonicalUnit(u string) string {
	if c, ok := unitAliases[u]; ok {
		return c
	}
	return u
}

// unitCompatible returns true when the two unit strings refer to the same unit.
func unitCompatible(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ca, cb := unitAliases[a], unitAliases[b]
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
// When CustomerPartNumber is empty, ManufacturerPartNumber is used as the key.
func applyMapping(row *BOMRow, ms mappingReader) {
	if ms == nil {
		return
	}
	key := row.CustomerPartNumber
	if key == "" {
		key = row.ManufacturerPartNumber
	}
	if key == "" {
		return
	}
	m, ok := ms.lookup(key)
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
