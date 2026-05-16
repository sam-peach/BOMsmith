package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDigiKey fakes the two Digi-Key endpoints: the OAuth2 token exchange
// and the v4 keyword search. Token lifetimes are short (~10 min) so the
// refresh-buffer behaviour is exercised here.
type fakeDigiKey struct {
	tokenSrv     *httptest.Server
	apiSrv       *httptest.Server
	tokenHits    int
	apiHits      int
	nextResponse string
	lastHeaders  http.Header
}

func newFakeDigiKey(t *testing.T) *fakeDigiKey {
	t.Helper()
	f := &fakeDigiKey{}
	f.tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.tokenHits++
		_, _ = w.Write([]byte(`{"access_token":"dk-token","expires_in":599,"token_type":"Bearer"}`))
	}))
	f.apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.apiHits++
		f.lastHeaders = r.Header.Clone()
		if got := r.Header.Get("Authorization"); got != "Bearer dk-token" {
			t.Errorf("expected Bearer dk-token, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.nextResponse))
	}))
	t.Cleanup(func() { f.tokenSrv.Close(); f.apiSrv.Close() })
	return f
}

func (f *fakeDigiKey) provider() *digikeyProvider {
	return &digikeyProvider{
		clientID:     "dk-id",
		clientSecret: "dk-secret",
		tokenURL:     f.tokenSrv.URL,
		searchURL:    f.apiSrv.URL,
		httpClient:   http.DefaultClient,
	}
}

// Digi-Key v4 nests pricing under ProductVariations (one per packaging:
// cut-tape, reel, tube). Each variation has its own DigiKeyProductNumber
// and StandardPricing ladder — we emit one SupplierOffer per variation so
// the (mpn, supplier, sku, currency) cache key keeps them distinct.
func TestDigiKeyProvider_EmitsOnePerVariation(t *testing.T) {
	f := newFakeDigiKey(t)
	// Real shape (verified live): ManufacturerLeadWeeks is a bare number
	// string in *weeks*; stock is per-variation QuantityAvailableforPackageType
	// (the product-level total is absent/misleading).
	f.nextResponse = `{
		"Products": [{
			"ManufacturerProductNumber": "STM32F103C8T6",
			"Manufacturer": { "Name": "STMicroelectronics" },
			"ProductUrl": "https://www.digikey.co.uk/en/products/detail/x",
			"ManufacturerLeadWeeks": "16",
			"ProductVariations": [
				{
					"DigiKeyProductNumber": "497-6063-1-ND",
					"PackageType": { "Name": "Cut Tape (CT)" },
					"QuantityAvailableforPackageType": 54321,
					"StandardPricing": [
						{ "BreakQuantity": 1, "UnitPrice": 2.34 },
						{ "BreakQuantity": 10, "UnitPrice": 2.05 }
					]
				},
				{
					"DigiKeyProductNumber": "497-6063-2-ND",
					"PackageType": { "Name": "Tape & Reel (TR)" },
					"QuantityAvailableforPackageType": 200,
					"StandardPricing": [
						{ "BreakQuantity": 250, "UnitPrice": 1.40 }
					]
				}
			]
		}]
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 2, "two packaging variations → two offers")

	assert.Equal(t, "DigiKey", offers[0].Supplier)
	assert.Equal(t, "497-6063-1-ND", offers[0].SKU)
	assert.Equal(t, "digikey", offers[0].Source)
	assert.Equal(t, "GBP", offers[0].Currency)
	require.NotNil(t, offers[0].Stock)
	assert.Equal(t, 54321, *offers[0].Stock, "stock is the per-variation QuantityAvailableforPackageType")
	require.NotNil(t, offers[0].LeadTimeDays)
	assert.Equal(t, 112, *offers[0].LeadTimeDays, "ManufacturerLeadWeeks 16 → 16×7 = 112 days")
	require.Len(t, offers[0].PriceBreaks, 2)
	assert.InDelta(t, 2.34, offers[0].PriceBreaks[0].Price, 1e-9)

	assert.Equal(t, "497-6063-2-ND", offers[1].SKU)
	require.NotNil(t, offers[1].Stock)
	assert.Equal(t, 200, *offers[1].Stock)
	require.Len(t, offers[1].PriceBreaks, 1)
	assert.Equal(t, 250, offers[1].PriceBreaks[0].Quantity)
}

// The locale/currency + client-id headers are mandatory; without them
// Digi-Key returns USD or 401s. Lock them in.
func TestDigiKeyProvider_SendsLocaleAndClientHeaders(t *testing.T) {
	f := newFakeDigiKey(t)
	f.nextResponse = `{"Products":[]}`

	_, _ = f.provider().priceByMPN(context.Background(), "X", "GBP")

	assert.Equal(t, "GBP", f.lastHeaders.Get("X-DIGIKEY-Locale-Currency"))
	assert.Equal(t, "dk-id", f.lastHeaders.Get("X-DIGIKEY-Client-Id"))
	assert.NotEmpty(t, f.lastHeaders.Get("X-DIGIKEY-Locale-Site"))
}

// Token is fetched once and reused across calls (Digi-Key tokens are
// short-lived but a single pricing run shouldn't re-auth per row).
func TestDigiKeyProvider_TokenCachedAcrossCalls(t *testing.T) {
	f := newFakeDigiKey(t)
	f.nextResponse = `{"Products":[]}`
	p := f.provider()

	_, _ = p.priceByMPN(context.Background(), "A", "GBP")
	_, _ = p.priceByMPN(context.Background(), "B", "GBP")

	assert.Equal(t, 1, f.tokenHits)
	assert.Equal(t, 2, f.apiHits)
}

// Empty Products array → (nil, nil): no coverage, not an error.
func TestDigiKeyProvider_NoProductsReturnsNilNoError(t *testing.T) {
	f := newFakeDigiKey(t)
	f.nextResponse = `{"Products":[]}`

	offers, err := f.provider().priceByMPN(context.Background(), "NOPE", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// Non-200 from the search endpoint must bubble as an error (transport
// failure, not "no coverage").
func TestDigiKeyProvider_HTTPErrorBubbles(t *testing.T) {
	f := newFakeDigiKey(t)
	f.apiSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"token expired","status":401}`))
	})

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	assert.Error(t, err)
	assert.Nil(t, offers)
}

// Digi-Key suffixes packaging onto the MPN, so a search for "…T6" returns
// both "…T6" (often 0-stock Tray) and "…T6TR" (the in-stock reel). The
// match must keep BOTH (exact + prefix) — discarding the TR product was a
// real bug that hid all the stock — while still rejecting unrelated "…T7".
func TestDigiKeyProvider_MatchesExactAndPackagingSuffix(t *testing.T) {
	f := newFakeDigiKey(t)
	f.nextResponse = `{
		"Products": [
			{"ManufacturerProductNumber":"STM32F103C8T6","ProductVariations":[{"DigiKeyProductNumber":"TRAY","QuantityAvailableforPackageType":0,"StandardPricing":[{"BreakQuantity":1,"UnitPrice":1.0}]}]},
			{"ManufacturerProductNumber":"STM32F103C8T6TR","ProductVariations":[{"DigiKeyProductNumber":"REEL","QuantityAvailableforPackageType":4102,"StandardPricing":[{"BreakQuantity":1,"UnitPrice":0.9}]}]},
			{"ManufacturerProductNumber":"STM32F103C8T7","ProductVariations":[{"DigiKeyProductNumber":"NO","StandardPricing":[{"BreakQuantity":1,"UnitPrice":9.0}]}]}
		]
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 2, "exact + packaging-suffix kept; unrelated …T7 dropped")
	skus := map[string]bool{}
	for _, o := range offers {
		skus[o.SKU] = true
	}
	assert.True(t, skus["TRAY"] && skus["REEL"], "both the exact and the …TR packaging product must survive")
	assert.False(t, skus["NO"], "unrelated …T7 must be rejected")
}

var _ pricingProvider = (*digikeyProvider)(nil)
