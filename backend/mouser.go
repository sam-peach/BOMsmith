package main

// Mouser pricing provider — implements pricingProvider against the Mouser
// Search API v1. Auth is a bare apiKey query param (no OAuth), so this is
// the simplest of the direct-distributor providers.
//
// Quirks the parsing has to absorb:
//   - PriceBreaks[].Price is a locale-formatted STRING ("£2.34", "1,23 €"),
//     not a number — see parseLocalizedPrice.
//   - Availability / LeadTime are free text ("1234 In Stock", "10 Days") —
//     see parseLeadingInt.
//   - Business errors come back with HTTP 200 in a top-level Errors array.
//   - The search returns manufacturer near-misses; we keep only the exact
//     (case-insensitive) MPN match so a fuzzy hit can't pollute pricing.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const mouserSearchEndpoint = "https://api.mouser.com/api/v1/search/partnumber"

type mouserProvider struct {
	apiKey     string
	searchURL  string // overridable for tests
	httpClient *http.Client
}

func newMouserProvider(apiKey string) *mouserProvider {
	return &mouserProvider{
		apiKey:     apiKey,
		searchURL:  mouserSearchEndpoint,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (m *mouserProvider) name() string { return "mouser" }

func (m *mouserProvider) priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error) {
	mpn = strings.TrimSpace(mpn)
	if mpn == "" {
		return nil, nil
	}

	reqBody, _ := json.Marshal(map[string]any{
		"SearchByPartRequest": map[string]any{
			"mouserPartNumber":  mpn,
			"partSearchOptions": "string",
		},
	})
	u := m.searchURL + "?apiKey=" + m.apiKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("mouser: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mouser: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mouser: status %d: %s", resp.StatusCode, string(b))
	}

	var parsed mouserSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("mouser: decode: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("mouser: api error: %s", parsed.Errors[0].Message)
	}
	if parsed.SearchResults == nil || len(parsed.SearchResults.Parts) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	want := strings.ToUpper(mpn)
	var offers []SupplierOffer
	for _, p := range parsed.SearchResults.Parts {
		// Drop manufacturer near-misses — only the exact MPN counts.
		if strings.ToUpper(strings.TrimSpace(p.ManufacturerPartNumber)) != want {
			continue
		}
		if len(p.PriceBreaks) == 0 {
			continue
		}
		out := SupplierOffer{
			Supplier:    normaliseSupplierName("Mouser"),
			SKU:         p.MouserPartNumber,
			SupplierURL: p.ProductDetailURL,
			Source:      "mouser",
			Currency:    currency,
			FetchedAt:   now,
			Stock:       parseLeadingInt(p.Availability),
			LeadTimeDays: parseLeadingInt(p.LeadTime),
		}
		for _, b := range p.PriceBreaks {
			price, perr := parseLocalizedPrice(b.Price)
			if perr != nil {
				continue // skip an unparseable break rather than poison the offer
			}
			out.PriceBreaks = append(out.PriceBreaks, PriceBreak{Quantity: b.Quantity, Price: price})
		}
		if len(out.PriceBreaks) == 0 {
			continue
		}
		offers = append(offers, out)
	}
	return offers, nil
}

// parseLocalizedPrice turns Mouser's stringly-typed, locale-formatted price
// into a float. Handles currency symbols/codes, US ("1,234.56") and
// European ("1.234,56") grouping, and the decimal-comma convention.
//
// Disambiguation rule when only one separator type is present: a single
// comma (or dot) followed by exactly 3 digits and preceded by a digit is
// treated as a thousands separator; otherwise it's the decimal point.
func parseLocalizedPrice(s string) (float64, error) {
	// Keep only digits and the two separator characters.
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' || r == '-' {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if clean == "" || clean == "-" {
		return 0, fmt.Errorf("parseLocalizedPrice: no numeric content in %q", s)
	}

	lastDot := strings.LastIndex(clean, ".")
	lastComma := strings.LastIndex(clean, ",")

	switch {
	case lastDot >= 0 && lastComma >= 0:
		// Both present: the rightmost is the decimal separator.
		if lastComma > lastDot {
			clean = strings.ReplaceAll(clean, ".", "")
			clean = strings.Replace(clean, ",", ".", 1)
			clean = strings.ReplaceAll(clean, ",", "")
		} else {
			clean = strings.ReplaceAll(clean, ",", "")
		}
	case lastComma >= 0:
		// Only commas. 3 trailing digits with a leading digit ⇒ thousands.
		frac := len(clean) - lastComma - 1
		if frac == 3 && lastComma > 0 {
			clean = strings.ReplaceAll(clean, ",", "")
		} else {
			clean = strings.Replace(clean, ",", ".", 1)
		}
	case lastDot >= 0:
		// Only dots. Same heuristic for a stray thousands dot.
		frac := len(clean) - lastDot - 1
		if frac == 3 && strings.Count(clean, ".") == 1 && lastDot > 0 && len(clean)-1 != lastDot+3 {
			// keep as decimal — frac==3 is normal for prices like 1.234? rare.
		}
		clean = collapseExtraDots(clean)
	}

	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, fmt.Errorf("parseLocalizedPrice: %q → %q: %w", s, clean, err)
	}
	return v, nil
}

// collapseExtraDots removes all but the last dot, so "1.234.567" (grouping)
// becomes "1234.567". A single dot is left untouched.
func collapseExtraDots(s string) string {
	if strings.Count(s, ".") <= 1 {
		return s
	}
	last := strings.LastIndex(s, ".")
	return strings.ReplaceAll(s[:last], ".", "") + s[last:]
}

// parseLeadingInt extracts the integer prefix of a free-text field
// ("1234 In Stock" → 1234, "10 Days" → 10). Returns nil when there is no
// leading integer ("In Stock", "", "None") so callers can leave the
// pointer field unset rather than asserting a fake 0.
func parseLeadingInt(s string) *int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return nil
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return nil
	}
	return &n
}

type mouserSearchResponse struct {
	Errors []struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	} `json:"Errors"`
	SearchResults *struct {
		NumberOfResult int `json:"NumberOfResult"`
		Parts          []struct {
			MouserPartNumber       string `json:"MouserPartNumber"`
			ManufacturerPartNumber string `json:"ManufacturerPartNumber"`
			Manufacturer           string `json:"Manufacturer"`
			Availability           string `json:"Availability"`
			LeadTime               string `json:"LeadTime"`
			ProductDetailURL       string `json:"ProductDetailUrl"`
			PriceBreaks            []struct {
				Quantity int    `json:"Quantity"`
				Price    string `json:"Price"`
				Currency string `json:"Currency"`
			} `json:"PriceBreaks"`
		} `json:"Parts"`
	} `json:"SearchResults"`
}
