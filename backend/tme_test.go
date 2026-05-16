package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTME routes the two signed endpoints TME needs (Search to resolve a
// symbol from the MPN, then GetPricesAndStocks) off one httptest server.
type fakeTME struct {
	srv          *httptest.Server
	searchResp   string
	pricesResp   string
	gotSignature string
	searchHits   int
	pricesHits   int
}

func newFakeTME(t *testing.T) *fakeTME {
	t.Helper()
	f := &fakeTME{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.gotSignature = r.PostFormValue("ApiSignature")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "Search"):
			f.searchHits++
			_, _ = w.Write([]byte(f.searchResp))
		case strings.Contains(r.URL.Path, "GetPricesAndStocks"):
			f.pricesHits++
			_, _ = w.Write([]byte(f.pricesResp))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTME) provider() *tmeProvider {
	return &tmeProvider{
		token:      "tme-token",
		appSecret:  "tme-secret",
		baseURL:    f.srv.URL,
		httpClient: http.DefaultClient,
	}
}

// The signature base string is a security contract with TME — its exact
// shape (method, raw-encoded URL, sorted raw-encoded params, ampersand
// joins) must not drift. Pin the literal so any change to ordering or
// encoding fails loudly.
func TestTMESignatureBase_ExactContract(t *testing.T) {
	params := url.Values{}
	params.Set("Token", "tme-token")
	params.Set("Country", "GB")
	params.Set("Language", "EN")
	params.Set("SearchPlain", "STM32 F103") // space must encode as %20, not +

	base := tmeSignatureBase(http.MethodPost, "https://api.tme.eu/Products/Search.json", params)

	// Params sorted by key; values raw-encoded (space → %20); the whole
	// param string and the URL each raw-encoded once more into the base.
	wantSortedParams := "Country=GB&Language=EN&SearchPlain=STM32%20F103&Token=tme-token"
	wantBase := "POST&" +
		rawEncode("https://api.tme.eu/Products/Search.json") + "&" +
		rawEncode(wantSortedParams)
	assert.Equal(t, wantBase, base)
}

// tmeSign must be a deterministic HMAC-SHA1 → base64 of the base string.
// We recompute it independently in-test so a regression in the crypto
// wiring (wrong hash, wrong key order, hex vs base64) is caught.
func TestTMESign_MatchesIndependentHMAC(t *testing.T) {
	base := "POST&https%3A%2F%2Fexample&Foo%3Dbar"
	got := tmeSign("the-secret", base)

	mac := hmac.New(sha1.New, []byte("the-secret"))
	mac.Write([]byte(base))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, got)
}

// rawEncode must match PHP rawurlencode / RFC3986: space → %20 (not +),
// and the unreserved set -_.~ left untouched. Go's url.QueryEscape gets
// both of these wrong, which is exactly why we hand-roll it.
func TestRawEncode_RFC3986(t *testing.T) {
	assert.Equal(t, "a%20b", rawEncode("a b"))
	assert.Equal(t, "a-b_c.d~e", rawEncode("a-b_c.d~e"))
	assert.Equal(t, "%2B%3D%26", rawEncode("+=&"))
	assert.Equal(t, "STM32F103C8T6", rawEncode("STM32F103C8T6"))
}

// Happy path: Search resolves the MPN to a TME symbol, then
// GetPricesAndStocks returns the price ladder + stock. The provider must
// chain both signed calls and flatten the result.
func TestTMEProvider_ResolvesSymbolThenPrices(t *testing.T) {
	f := newFakeTME(t)
	f.searchResp = `{"Status":"OK","Data":{"ProductList":[
		{"Symbol":"STM32F103C8T6","OriginalSymbol":"STM32F103C8T6","Producer":"STM"}
	]}}`
	f.pricesResp = `{"Status":"OK","Data":{"Currency":"GBP","ProductList":[
		{"Symbol":"STM32F103C8T6","Unit":"pcs","AmountInStock":1730,
		 "PriceList":[{"Amount":1,"PriceValue":2.55},{"Amount":10,"PriceValue":2.20}]}
	]}}`

	offers, err := f.provider().priceByMPN(context.Background(), "STM32F103C8T6", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1)
	o := offers[0]
	assert.Equal(t, "TME", o.Supplier)
	assert.Equal(t, "STM32F103C8T6", o.SKU)
	assert.Equal(t, "tme", o.Source)
	assert.Equal(t, "GBP", o.Currency)
	assert.Contains(t, o.SupplierURL, "STM32F103C8T6")
	require.NotNil(t, o.Stock)
	assert.Equal(t, 1730, *o.Stock)
	require.Len(t, o.PriceBreaks, 2)
	assert.Equal(t, 1, o.PriceBreaks[0].Quantity)
	assert.InDelta(t, 2.55, o.PriceBreaks[0].Price, 1e-9)
	assert.NotEmpty(t, f.gotSignature, "every TME call must be signed")
	assert.Equal(t, 1, f.searchHits)
	assert.Equal(t, 1, f.pricesHits)
}

// Search finding nothing → (nil, nil), and we must NOT make the prices
// call (no symbol to price).
func TestTMEProvider_NoSearchMatchSkipsPricesCall(t *testing.T) {
	f := newFakeTME(t)
	f.searchResp = `{"Status":"OK","Data":{"ProductList":[]}}`

	offers, err := f.provider().priceByMPN(context.Background(), "NOPE", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
	assert.Equal(t, 0, f.pricesHits, "no symbol → must not call GetPricesAndStocks")
}

// A non-OK Status from TME is an API error and must bubble.
func TestTMEProvider_ErrorStatusBubbles(t *testing.T) {
	f := newFakeTME(t)
	f.searchResp = `{"Status":"E_INVALID_SIGNATURE","Error":"Invalid signature"}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	assert.Error(t, err)
	assert.Nil(t, offers)
}

var _ pricingProvider = (*tmeProvider)(nil)
