package main

// Farnell / element14 pricing provider — implements pricingProvider against
// the element14 Product Search API. Auth is a callInfo.apiKey query param.
//
// Currency is implied by the store, not a request parameter: uk.farnell.com
// prices in GBP, www.newark.com in USD, etc. We default to uk.farnell.com
// (Andrew's actual buying channel) and tag offers with the store's real
// currency so nothing is ever mislabelled. Non-GBP currencies fall back to
// the GBP store rather than guessing a mapping — a documented v1 limit.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const farnellSearchEndpoint = "https://api.element14.com/catalog/products"

type farnellProvider struct {
	apiKey        string
	searchURL     string // overridable for tests
	storeID       string // e.g. "uk.farnell.com" (fixes the price currency)
	storeCurrency string // the ISO code that storeID prices in
	httpClient    *http.Client
}

func newFarnellProvider(apiKey string) *farnellProvider {
	return &farnellProvider{
		apiKey:        apiKey,
		searchURL:     farnellSearchEndpoint,
		storeID:       "uk.farnell.com",
		storeCurrency: "GBP",
		httpClient:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (f *farnellProvider) name() string { return "farnell" }

func (f *farnellProvider) priceByMPN(ctx context.Context, mpn, _ string) ([]SupplierOffer, error) {
	mpn = strings.TrimSpace(mpn)
	if mpn == "" {
		return nil, nil
	}

	// element14 search-type prefix is `manuPartNum:` (NOT manuPartNumber:) —
	// the wrong prefix 400s. `resultsSettings.offset` is required alongside
	// numberOfResults. Verified against the live API + partner.element14.com
	// docs.
	q := url.Values{}
	q.Set("term", "manuPartNum:"+mpn)
	q.Set("storeInfo.id", f.storeID)
	q.Set("resultsSettings.responseGroup", "large") // includes prices + stock
	q.Set("resultsSettings.offset", "0")
	q.Set("resultsSettings.numberOfResults", "5")
	q.Set("callInfo.responseDataFormat", "json")
	q.Set("callInfo.apiKey", f.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.searchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("farnell: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("farnell: request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("farnell: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed farnellSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("farnell: decode: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("farnell: api error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}
	if parsed.Result == nil || len(parsed.Result.Products) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	want := strings.ToUpper(mpn)
	var offers []SupplierOffer
	for _, p := range parsed.Result.Products {
		// Keep exact MPN matches only (the search can fuzz).
		if mpnField := strings.ToUpper(strings.TrimSpace(p.TranslatedMPN)); mpnField != "" && mpnField != want {
			continue
		}
		if len(p.Prices) == 0 {
			continue
		}
		// The Search API does not return a product URL in any responseGroup,
		// so build a stable click-through from the order code (SKU). The
		// ?st= search resolves directly to the product page.
		out := SupplierOffer{
			Supplier:    normaliseSupplierName("Farnell"),
			SKU:         p.SKU,
			SupplierURL: fmt.Sprintf("https://%s/search?st=%s", f.storeID, url.QueryEscape(p.SKU)),
			Source:      "farnell",
			Currency:    f.storeCurrency,
			FetchedAt:   now,
		}
		if p.Stock != nil {
			if p.Stock.Level > 0 || p.Stock.LevelSet {
				lvl := p.Stock.Level
				out.Stock = &lvl
			}
			if p.Stock.LeastLeadTime > 0 {
				lt := p.Stock.LeastLeadTime
				out.LeadTimeDays = &lt
			}
		}
		for _, pr := range p.Prices {
			out.PriceBreaks = append(out.PriceBreaks, PriceBreak{Quantity: pr.From, Price: pr.Cost})
		}
		offers = append(offers, out)
	}
	return offers, nil
}

// farnellStock has a custom unmarshaller so we can tell "level present and
// zero" from "level absent" — a 0-stock part is still a real offer, just
// out of stock, and the panel dims rather than hides it.
type farnellStock struct {
	Level         int
	LevelSet      bool
	LeastLeadTime int
}

func (s *farnellStock) UnmarshalJSON(b []byte) error {
	var raw struct {
		Level         *int `json:"level"`
		LeastLeadTime int  `json:"leastLeadTime"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw.Level != nil {
		s.Level = *raw.Level
		s.LevelSet = true
	}
	s.LeastLeadTime = raw.LeastLeadTime
	return nil
}

type farnellSearchResponse struct {
	// A `manuPartNum:` search returns `manufacturerPartNumberSearchReturn`
	// (NOT manuPartNumberSearchReturn — verified against the live API).
	Result *struct {
		NumberOfResults int `json:"numberOfResults"`
		Products        []struct {
			SKU           string        `json:"sku"`
			DisplayName   string        `json:"displayName"`
			TranslatedMPN string        `json:"translatedManufacturerPartNumber"`
			Stock         *farnellStock `json:"stock"`
			Prices        []struct {
				From int     `json:"from"`
				To   *int    `json:"to"`
				Cost float64 `json:"cost"`
			} `json:"prices"`
		} `json:"products"`
	} `json:"manufacturerPartNumberSearchReturn"`
	// element14 reports errors as a non-200 with `{"error":{code,message}}`.
	// The status-code check handles those before this is consulted; this
	// field is the belt-and-braces case of a 200 carrying an error body.
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
