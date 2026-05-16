package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFarnell stands in for api.element14.com. Auth is a callInfo.apiKey
// query param; one GET endpoint.
type fakeFarnell struct {
	srv          *httptest.Server
	hits         int
	nextResponse string
	lastQuery    string
}

func newFakeFarnell(t *testing.T) *fakeFarnell {
	t.Helper()
	f := &fakeFarnell{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.nextResponse))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFarnell) provider() *farnellProvider {
	return &farnellProvider{
		apiKey:        "test-key",
		searchURL:     f.srv.URL,
		storeID:       "uk.farnell.com",
		storeCurrency: "GBP",
		httpClient:    http.DefaultClient,
	}
}

// A `manuPartNum:` search returns `manufacturerPartNumberSearchReturn` with
// numeric price breaks (from/to/cost) and nested stock. The API returns NO
// product URL in any responseGroup, so the provider synthesises a stable
// ?st= click-through from the order code. Shape verified against the live
// element14 API.
func TestFarnellProvider_ParsesOffer(t *testing.T) {
	f := newFakeFarnell(t)
	f.nextResponse = `{
		"manufacturerPartNumberSearchReturn": {
			"numberOfResults": 1,
			"products": [{
				"sku": "2467678",
				"displayName": "STM32F103C8T6 - MCU",
				"translatedManufacturerPartNumber": "STM32F103C8T6",
				"stock": { "level": 1300, "leastLeadTime": 5 },
				"prices": [
					{ "from": 1, "to": 9, "cost": 2.40 },
					{ "from": 10, "to": 99, "cost": 2.05 },
					{ "from": 100, "to": null, "cost": 1.72 }
				]
			}]
		}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1)
	o := offers[0]
	assert.Equal(t, "Farnell", o.Supplier)
	assert.Equal(t, "2467678", o.SKU)
	assert.Equal(t, "farnell", o.Source)
	assert.Equal(t, "GBP", o.Currency)
	assert.Equal(t, "https://uk.farnell.com/search?st=2467678", o.SupplierURL,
		"no productURL in the API response — must be synthesised from the SKU")
	require.NotNil(t, o.Stock)
	assert.Equal(t, 1300, *o.Stock)
	require.NotNil(t, o.LeadTimeDays)
	assert.Equal(t, 5, *o.LeadTimeDays)
	require.Len(t, o.PriceBreaks, 3)
	assert.Equal(t, 1, o.PriceBreaks[0].Quantity)
	assert.InDelta(t, 2.40, o.PriceBreaks[0].Price, 1e-9)
	assert.Equal(t, 100, o.PriceBreaks[2].Quantity)
	assert.InDelta(t, 1.72, o.PriceBreaks[2].Price, 1e-9)
}

// The request must carry the apiKey, the `manuPartNum:` term (NOT
// manuPartNumber: — the wrong prefix 400s), offset, the store id (which
// fixes the currency), and ask for JSON.
func TestFarnellProvider_BuildsCorrectQuery(t *testing.T) {
	f := newFakeFarnell(t)
	f.nextResponse = `{"manufacturerPartNumberSearchReturn":{"numberOfResults":0,"products":[]}}`

	_, _ = f.provider().priceByMPN(context.Background(), "ABC-123", "GBP")

	q := f.lastQuery
	assert.Contains(t, q, "callInfo.apiKey=test-key")
	assert.Contains(t, q, "manuPartNum%3AABC-123", "term must use the manuPartNum: prefix, URL-encoded")
	assert.NotContains(t, q, "manuPartNumber", "manuPartNumber: is the wrong prefix and 400s")
	assert.Contains(t, q, "resultsSettings.offset=0")
	assert.Contains(t, q, "storeInfo.id=uk.farnell.com")
	assert.Contains(t, q, "callInfo.responseDataFormat=json")
}

// Zero products → (nil, nil): no coverage, not an error.
func TestFarnellProvider_NoProductsReturnsNilNoError(t *testing.T) {
	f := newFakeFarnell(t)
	f.nextResponse = `{"manufacturerPartNumberSearchReturn":{"numberOfResults":0,"products":[]}}`

	offers, err := f.provider().priceByMPN(context.Background(), "NOPE", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// element14 reports a bad key / bad request as {"error":{code,message}}
// (e.g. 403 "Developer Inactive", 400 "Bad Request"). That must surface as
// a Go error so the handler doesn't cache an empty result. Real shape
// observed live during integration.
func TestFarnellProvider_ErrorBodyBubbles(t *testing.T) {
	f := newFakeFarnell(t)
	f.nextResponse = `{"error":{"code":403,"message":"Developer Inactive"}}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Developer Inactive")
	assert.Nil(t, offers)
}

// A product with no price breaks is dropped rather than emitted as a
// zero-price offer that would corrupt best-price maths downstream.
func TestFarnellProvider_DropsProductsWithNoPrices(t *testing.T) {
	f := newFakeFarnell(t)
	f.nextResponse = `{
		"manufacturerPartNumberSearchReturn": {
			"numberOfResults": 2,
			"products": [
				{"sku":"NO-PRICE","translatedManufacturerPartNumber":"X","prices":[]},
				{"sku":"HAS-PRICE","translatedManufacturerPartNumber":"X","prices":[{"from":1,"to":null,"cost":3.50}]}
			]
		}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1)
	assert.Equal(t, "HAS-PRICE", offers[0].SKU)
}

var _ pricingProvider = (*farnellProvider)(nil)
