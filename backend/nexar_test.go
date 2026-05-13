package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNexar returns httptest servers covering the two endpoints the client
// uses: the OAuth token exchange and the GraphQL endpoint. Tests inject the
// URLs into the nexarProvider via the exported config fields.
type fakeNexar struct {
	tokenSrv  *httptest.Server
	apiSrv    *httptest.Server
	tokenHits int
	apiHits   int
	// nextGraphQLResponse is the raw JSON body returned by the GraphQL
	// endpoint on the next call. Set per-test.
	nextGraphQLResponse string
}

func newFakeNexar(t *testing.T) *fakeNexar {
	t.Helper()
	f := &fakeNexar{}
	f.tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.tokenHits++
		_, _ = fmt.Fprint(w, `{"access_token":"fake-token","expires_in":86400,"token_type":"Bearer"}`)
	}))
	f.apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.apiHits++
		if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
			t.Errorf("expected Bearer auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, f.nextGraphQLResponse)
	}))
	t.Cleanup(func() {
		f.tokenSrv.Close()
		f.apiSrv.Close()
	})
	return f
}

func (f *fakeNexar) provider() *nexarProvider {
	return &nexarProvider{
		clientID:     "test-id",
		clientSecret: "test-secret",
		tokenURL:     f.tokenSrv.URL,
		graphqlURL:   f.apiSrv.URL,
		httpClient:   http.DefaultClient,
	}
}

// A typical Nexar response has one part with multiple sellers, each with
// one or more offers carrying a price-break ladder. The client must turn
// that nested structure into a flat []SupplierOffer keyed by supplier.
func TestNexarProvider_ParsesOffersFromGraphQL(t *testing.T) {
	f := newFakeNexar(t)
	f.nextGraphQLResponse = `{
		"data": {
			"supSearchMpn": {
				"results": [{
					"part": {
						"mpn": "CF130.07.05.UL",
						"manufacturer": {"name": "Lapp"},
						"sellers": [
							{
								"company": {"name": "Farnell"},
								"offers": [{
									"sku": "1234567",
									"inventoryLevel": 4200,
									"factoryLeadDays": 14,
									"clickUrl": "https://uk.farnell.com/lapp/cf130/dp/1234567",
									"prices": [
										{"quantity": 1, "convertedPrice": 2.34, "convertedCurrency": "GBP"},
										{"quantity": 100, "convertedPrice": 1.95, "convertedCurrency": "GBP"}
									]
								}]
							},
							{
								"company": {"name": "RS"},
								"offers": [{
									"sku": "RS-8888",
									"inventoryLevel": 800,
									"factoryLeadDays": 21,
									"clickUrl": "https://uk.rs-online.com/RS-8888",
									"prices": [
										{"quantity": 1, "convertedPrice": 2.40, "convertedCurrency": "GBP"}
									]
								}]
							}
						]
					}
				}]
			}
		}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "CF130.07.05.UL", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 2, "two sellers → two offers")

	// Suppliers should appear in the order returned by Nexar (no sort).
	assert.Equal(t, "Farnell", offers[0].Supplier)
	assert.Equal(t, "1234567", offers[0].SKU)
	assert.Equal(t, "nexar", offers[0].Source)
	assert.Equal(t, "GBP", offers[0].Currency)
	require.NotNil(t, offers[0].Stock)
	assert.Equal(t, 4200, *offers[0].Stock)
	require.NotNil(t, offers[0].LeadTimeDays)
	assert.Equal(t, 14, *offers[0].LeadTimeDays)
	require.Len(t, offers[0].PriceBreaks, 2)
	assert.Equal(t, 1, offers[0].PriceBreaks[0].Quantity)
	assert.InDelta(t, 2.34, offers[0].PriceBreaks[0].Price, 1e-9)
	assert.Equal(t, 100, offers[0].PriceBreaks[1].Quantity)
	assert.InDelta(t, 1.95, offers[0].PriceBreaks[1].Price, 1e-9)

	assert.Equal(t, "RS", offers[1].Supplier)
}

// An MPN Octopart doesn't know about returns an empty results array — this
// must turn into (nil, nil), not an error. The handler distinguishes
// "no coverage" from "transport failure" by the error value.
func TestNexarProvider_EmptyResultsReturnsNilWithoutError(t *testing.T) {
	f := newFakeNexar(t)
	f.nextGraphQLResponse = `{"data":{"supSearchMpn":{"results":[]}}}`

	offers, err := f.provider().priceByMPN(context.Background(), "NEVER-HEARD-OF", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// Sellers with zero offers must not produce a zero-priceBreak SupplierOffer.
// (Nexar occasionally returns a seller record with no current offers — a
// "we knew this supplier carried this part once" signal — and ingesting
// those as zero-price offers would corrupt the best-price computation.)
func TestNexarProvider_DropsSellersWithNoOffers(t *testing.T) {
	f := newFakeNexar(t)
	f.nextGraphQLResponse = `{
		"data": {"supSearchMpn": {"results": [{
			"part": {"mpn": "X", "sellers": [
				{"company": {"name": "Ghost"}, "offers": []},
				{"company": {"name": "Real"}, "offers": [{
					"sku": "R1", "prices": [{"quantity": 1, "convertedPrice": 1.0, "convertedCurrency": "GBP"}]
				}]}
			]}
		}]}}
	}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 1, "the Ghost seller had no offers and must be dropped")
	assert.Equal(t, "Real", offers[0].Supplier)
}

// GraphQL errors at the protocol level (non-200, or a populated "errors"
// array on the response) must surface as Go errors so the handler can
// distinguish "no offers" from "Nexar broke" and fall through to the
// CSV fallback / pricing_unavailable flag rather than caching emptiness.
func TestNexarProvider_GraphQLErrorsBubble(t *testing.T) {
	f := newFakeNexar(t)
	f.nextGraphQLResponse = `{"errors":[{"message":"rate limited"}]}`

	offers, err := f.provider().priceByMPN(context.Background(), "X", "GBP")
	assert.Error(t, err)
	assert.Nil(t, offers)
}

// Token fetch happens at most once when many calls are in flight, otherwise
// we'd burn the OAuth quota. Verify by making two consecutive calls and
// asserting the token endpoint was hit exactly once.
func TestNexarProvider_TokenCachedAcrossCalls(t *testing.T) {
	f := newFakeNexar(t)
	f.nextGraphQLResponse = `{"data":{"supSearchMpn":{"results":[]}}}`

	p := f.provider()
	_, _ = p.priceByMPN(context.Background(), "X", "GBP")
	_, _ = p.priceByMPN(context.Background(), "Y", "GBP")

	assert.Equal(t, 1, f.tokenHits, "token endpoint must be hit at most once across calls")
	assert.Equal(t, 2, f.apiHits)
}

// The token request must include scope=supply.domain. Without it Nexar
// issues a token that lacks Supply API permissions and supSearchMpn comes
// back with a permission error — a silent regression class that's hard to
// notice in dev (returns "no offers" rather than a hard failure).
func TestNexarProvider_TokenRequestIncludesSupplyScope(t *testing.T) {
	f := newFakeNexar(t)
	captured := make(chan string, 1)
	f.tokenSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		captured <- r.PostForm.Get("scope")
		_, _ = fmt.Fprint(w, `{"access_token":"fake-token","expires_in":86400,"token_type":"Bearer"}`)
	})
	f.nextGraphQLResponse = `{"data":{"supSearchMpn":{"results":[]}}}`

	_, _ = f.provider().priceByMPN(context.Background(), "X", "GBP")

	got := <-captured
	assert.Equal(t, "supply.domain", got, "token request must request the Supply API scope")
}

// Sanity check on the GraphQL request body: the query must include the MPN
// and currency the operator asked for. The Nexar Production plan bills per
// call so getting this right matters.
func TestNexarProvider_SendsMPNAndCurrencyInQuery(t *testing.T) {
	f := newFakeNexar(t)
	captured := make(chan string, 1)
	f.apiSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured <- fmt.Sprintf("mpn=%v currency=%v", body.Variables["mpn"], body.Variables["currency"])
		_, _ = fmt.Fprint(w, `{"data":{"supSearchMpn":{"results":[]}}}`)
	})

	_, _ = f.provider().priceByMPN(context.Background(), "ABC-123", "USD")

	got := <-captured
	assert.Equal(t, "mpn=ABC-123 currency=USD", got)
}
