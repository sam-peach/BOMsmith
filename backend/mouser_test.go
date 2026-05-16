package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMouser stands in for api.mouser.com. Mouser auth is a bare apiKey
// query param (no OAuth), so there's a single endpoint to fake.
type fakeMouser struct {
	srv          *httptest.Server
	hits         int
	nextResponse string
	lastBody     string
	lastQuery    string
}

func newFakeMouser(t *testing.T) *fakeMouser {
	t.Helper()
	f := &fakeMouser{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		f.lastBody = string(buf)
		f.lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.nextResponse))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMouser) provider() *mouserProvider {
	return &mouserProvider{
		apiKey:     "test-key",
		searchURL:  f.srv.URL,
		httpClient: http.DefaultClient,
	}
}

// A typical Mouser response: one part, a price-break ladder where prices
// are LOCALE-FORMATTED STRINGS ("£2.34", "1,23 €"), availability and lead
// time as free text ("1234 In Stock", "10 Days"). The provider must turn
// all of that into a clean SupplierOffer.
func TestMouserProvider_ParsesOffer(t *testing.T) {
	f := newFakeMouser(t)
	f.nextResponse = `{
		"Errors": [],
		"SearchResults": {
			"NumberOfResult": 1,
			"Parts": [{
				"MouserPartNumber": "511-STM32F103C8T6",
				"ManufacturerPartNumber": "STM32F103C8T6",
				"Manufacturer": "STMicroelectronics",
				"Availability": "1234 In Stock",
				"LeadTime": "10 Days",
				"ProductDetailUrl": "https://www.mouser.co.uk/pn/511",
				"PriceBreaks": [
					{"Quantity": 1, "Price": "£2.34", "Currency": "GBP"},
					{"Quantity": 100, "Price": "£1.95", "Currency": "GBP"}
				]
			}]
		}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1)
	o := offers[0]
	assert.Equal(t, "Mouser", o.Supplier)
	assert.Equal(t, "511-STM32F103C8T6", o.SKU)
	assert.Equal(t, "mouser", o.Source)
	assert.Equal(t, "GBP", o.Currency)
	assert.Equal(t, "https://www.mouser.co.uk/pn/511", o.SupplierURL)
	require.NotNil(t, o.Stock)
	assert.Equal(t, 1234, *o.Stock)
	require.NotNil(t, o.LeadTimeDays)
	assert.Equal(t, 10, *o.LeadTimeDays)
	require.Len(t, o.PriceBreaks, 2)
	assert.Equal(t, 1, o.PriceBreaks[0].Quantity)
	assert.InDelta(t, 2.34, o.PriceBreaks[0].Price, 1e-9)
	assert.Equal(t, 100, o.PriceBreaks[1].Quantity)
	assert.InDelta(t, 1.95, o.PriceBreaks[1].Price, 1e-9)
}

// The apiKey must travel as a query param and the MPN in the request body —
// getting either wrong means every call silently 401s or returns nothing.
func TestMouserProvider_SendsApiKeyAndMPN(t *testing.T) {
	f := newFakeMouser(t)
	f.nextResponse = `{"Errors":[],"SearchResults":{"NumberOfResult":0,"Parts":[]}}`

	_, _ = f.provider().priceByMPN(context.Background(), "ABC-123", "GBP")

	assert.Contains(t, f.lastQuery, "apiKey=test-key")
	assert.Contains(t, f.lastBody, "ABC-123")
}

// Mouser reports business errors in a top-level Errors array with a 200
// status — these must surface as Go errors so the handler treats them as
// transport failure (don't cache emptiness), not "no coverage".
func TestMouserProvider_BusinessErrorBubbles(t *testing.T) {
	f := newFakeMouser(t)
	f.nextResponse = `{"Errors":[{"Code":"InvalidApiKey","Message":"API key is invalid"}],"SearchResults":null}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	assert.Error(t, err)
	assert.Nil(t, offers)
}

// Zero results is "no coverage", not an error: (nil, nil).
func TestMouserProvider_NoResultsReturnsNilNoError(t *testing.T) {
	f := newFakeMouser(t)
	f.nextResponse = `{"Errors":[],"SearchResults":{"NumberOfResult":0,"Parts":[]}}`

	offers, err := f.provider().priceByMPN(context.Background(), "NOPE", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// Mouser returns one Part per manufacturer match; we want the exact MPN
// match, not a near-miss the search heuristic threw in. Parts whose
// ManufacturerPartNumber doesn't match (case-insensitively) are dropped.
func TestMouserProvider_FiltersToExactMPNMatch(t *testing.T) {
	f := newFakeMouser(t)
	f.nextResponse = `{
		"Errors": [],
		"SearchResults": {
			"NumberOfResult": 2,
			"Parts": [
				{"MouserPartNumber":"A","ManufacturerPartNumber":"stm32f103c8t6","PriceBreaks":[{"Quantity":1,"Price":"£1.00","Currency":"GBP"}]},
				{"MouserPartNumber":"B","ManufacturerPartNumber":"STM32F103C8T6-OTHER","PriceBreaks":[{"Quantity":1,"Price":"£9.00","Currency":"GBP"}]}
			]
		}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1, "only the exact (case-insensitive) MPN match should survive")
	assert.Equal(t, "A", offers[0].SKU)
}

// parseLocalizedPrice is the workhorse for Mouser's stringly-typed prices.
// It must cope with currency symbols/codes, thousands separators, and the
// European decimal-comma convention without mangling the value.
func TestParseLocalizedPrice(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"£2.34", 2.34},
		{"$2.34", 2.34},
		{"2.34", 2.34},
		{"€2,34", 2.34},        // European decimal comma
		{"2,34 €", 2.34},       // trailing currency, decimal comma
		{"1,234.56", 1234.56},  // US thousands + decimal
		{"1.234,56 €", 1234.56}, // European thousands + decimal
		{"1,234", 1234},        // ambiguous: 3 trailing digits → thousands sep
		{"0.0261", 0.0261},
		{"£1,000.00", 1000.00},
	}
	for _, tc := range cases {
		got, err := parseLocalizedPrice(tc.in)
		require.NoError(t, err, "input=%q", tc.in)
		assert.InDelta(t, tc.want, got, 1e-6, "input=%q", tc.in)
	}

	_, err := parseLocalizedPrice("")
	assert.Error(t, err, "empty string must error, not silently return 0")
	_, err = parseLocalizedPrice("Quote")
	assert.Error(t, err, `non-numeric ("Quote") must error rather than coerce to 0`)
}

// parseLeadingInt pulls the integer prefix out of Mouser's free-text
// Availability / LeadTime strings ("1234 In Stock", "10 Days").
func TestParseLeadingInt(t *testing.T) {
	cases := []struct {
		in   string
		want *int
	}{
		{"1234 In Stock", intp(1234)},
		{"10 Days", intp(10)},
		{"0", intp(0)},
		{"", nil},
		{"In Stock", nil},
		{"None", nil},
	}
	for _, tc := range cases {
		got := parseLeadingInt(tc.in)
		if tc.want == nil {
			assert.Nil(t, got, "input=%q", tc.in)
		} else {
			require.NotNil(t, got, "input=%q", tc.in)
			assert.Equal(t, *tc.want, *got, "input=%q", tc.in)
		}
	}
}

func intp(n int) *int { return &n }

// Compile-time interface check.
var _ pricingProvider = (*mouserProvider)(nil)

// Keep the json import meaningful even if the fixtures change.
var _ = json.Marshal
