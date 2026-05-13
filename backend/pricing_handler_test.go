package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPricingServer is the shared fixture for /api/documents/{id}/price tests.
// It wires the in-memory pricing collaborators onto a server prepared by
// newSettingsServer (which carries the auth helpers).
func newPricingServer(t *testing.T) (*server, *stubPricingProvider, *memPriceCache, *memPricingRuns, string) {
	t.Helper()
	srv, token := newSettingsServer(t)
	cache := newMemPriceCache()
	runs := newMemPricingRuns()
	provider := newStubPricingProvider()
	srv.priceCache = cache
	srv.priceProvider = provider
	srv.pricingRuns = runs
	srv.pricingCacheTTL = 24 * time.Hour
	srv.pricingCurrency = "GBP"
	return srv, provider, cache, runs, token
}

func priceableRow(id string, mpn string) BOMRow {
	q := 1.0
	return BOMRow{
		ID:                     id,
		LineNumber:             1,
		ManufacturerPartNumber: mpn,
		Quantity:               Quantity{Raw: "1", Value: &q, Normalized: &q, Flags: []string{}},
		Flags:                  []string{},
		ConfirmedFields:        []string{"manufacturerPartNumber"},
	}
}

func seedDoc(srv *server, doc *Document) {
	if doc.Warnings == nil {
		doc.Warnings = []string{}
	}
	srv.store.save(doc)
}

// Happy path: one row, one cache miss → provider is called once, the offer
// gets cached, the row picks up best-price decoration, and the run records
// 1 priced row + 0 unavailable + 1 nexar call + 0 cache hits.
func TestPriceBOM_CacheMissCallsProviderAndCaches(t *testing.T) {
	srv, provider, cache, runs, token := newPricingServer(t)
	provider.set("MPN-1", []SupplierOffer{{
		Supplier: "Farnell", SKU: "F-1", Currency: "GBP", Source: "stub",
		PriceBreaks: []PriceBreak{{1, 1.50}, {100, 1.10}},
		FetchedAt:   time.Now().UTC(),
	}})
	seedDoc(srv, &Document{
		ID: "doc-1", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{priceableRow("row-1", "MPN-1")},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-1/price", "", token)
	req.SetPathValue("id", "doc-1")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp Document
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.BOMRows, 1)
	require.NotNil(t, resp.BOMRows[0].Pricing, "row should be decorated with pricing")
	require.NotNil(t, resp.BOMRows[0].Pricing.BestUnitPrice)
	assert.InDelta(t, 1.50, resp.BOMRows[0].Pricing.BestUnitPrice.Amount, 1e-9)

	assert.Equal(t, 1, provider.callCount())
	cached, ok := cache.get("MPN-1", "GBP", time.Hour)
	require.True(t, ok, "offer must be cached after a successful fetch")
	assert.Len(t, cached, 1)

	latest, err := runs.latest("doc-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, 1, latest.RowsTotal)
	assert.Equal(t, 1, latest.RowsPriced)
	assert.Equal(t, 0, latest.RowsUnavailable)
	assert.Equal(t, 0, latest.RowsSkipped)
	assert.Equal(t, 1, latest.NexarCallsMade)
	assert.Equal(t, 0, latest.CacheHits)
}

// Cache hits must NOT call the provider. This is the entire point of
// caching: keep API spend low. Counter on the run records the saving.
func TestPriceBOM_CacheHitSkipsProvider(t *testing.T) {
	srv, provider, cache, runs, token := newPricingServer(t)
	require.NoError(t, cache.put("MPN-2", []SupplierOffer{{
		Supplier: "DigiKey", SKU: "DK-2", Currency: "GBP", Source: "stub",
		PriceBreaks: []PriceBreak{{1, 2.00}},
		FetchedAt:   time.Now().UTC(),
	}}))
	seedDoc(srv, &Document{
		ID: "doc-2", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{priceableRow("row-1", "MPN-2")},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-2/price", "", token)
	req.SetPathValue("id", "doc-2")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, provider.callCount(), "cache hit must not call the provider")

	latest, _ := runs.latest("doc-2")
	require.NotNil(t, latest)
	assert.Equal(t, 1, latest.CacheHits)
	assert.Equal(t, 0, latest.NexarCallsMade)
}

// A confirmed MPN that neither cache nor provider can resolve must be
// flagged pricing_unavailable so the warnings panel surfaces it. The flag
// is appended to the row's existing Flags (never replaces them).
func TestPriceBOM_NoOffersSetsUnavailableFlag(t *testing.T) {
	srv, _, _, runs, token := newPricingServer(t)
	row := priceableRow("row-1", "MPN-MISSING")
	row.Flags = []string{"unit_ambiguous"} // pre-existing flag must survive
	seedDoc(srv, &Document{
		ID: "doc-3", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{row},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-3/price", "", token)
	req.SetPathValue("id", "doc-3")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	stored, err := srv.store.get("doc-3")
	require.NoError(t, err)
	require.Len(t, stored.BOMRows, 1)
	assert.Contains(t, stored.BOMRows[0].Flags, FlagPricingUnavailable)
	assert.Contains(t, stored.BOMRows[0].Flags, "unit_ambiguous", "pre-existing flags must be preserved")

	latest, _ := runs.latest("doc-3")
	require.NotNil(t, latest)
	assert.Equal(t, 1, latest.RowsUnavailable)
}

// Rows with no MPN are skipped, not flagged. The doc separates "we tried
// and got nothing" (flag) from "we couldn't try" (skipped count) — the
// latter is the operator's signal to go back and confirm an MPN.
func TestPriceBOM_RowsWithoutMPNAreSkipped(t *testing.T) {
	srv, provider, _, runs, token := newPricingServer(t)
	q := 1.0
	row := BOMRow{
		ID: "row-1", LineNumber: 1,
		ManufacturerPartNumber: "", // empty MPN
		Quantity:               Quantity{Raw: "1", Value: &q, Flags: []string{}},
		Flags:                  []string{},
		ConfirmedFields:        []string{},
	}
	seedDoc(srv, &Document{
		ID: "doc-4", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{row},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-4/price", "", token)
	req.SetPathValue("id", "doc-4")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, provider.callCount(), "no MPN → no provider call")

	stored, _ := srv.store.get("doc-4")
	assert.NotContains(t, stored.BOMRows[0].Flags, FlagPricingUnavailable,
		"skipped rows must not be flagged — flag means 'we tried and failed'")

	latest, _ := runs.latest("doc-4")
	require.NotNil(t, latest)
	assert.Equal(t, 1, latest.RowsSkipped)
}

// 404 on unknown document — matches the pattern of every other
// /api/documents/{id}/... handler.
func TestPriceBOM_MissingDocumentReturns404(t *testing.T) {
	srv, _, _, _, token := newPricingServer(t)
	req := authedRequest(http.MethodPost, "/api/documents/nope/price", "", token)
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// 503 when no priceProvider is configured. Without one, the system can't
// price anything; a hard failure is the right answer rather than silently
// flagging everything unavailable. (Mock provider is the local-dev escape.)
func TestPriceBOM_NoProviderReturns503(t *testing.T) {
	srv, _, _, _, token := newPricingServer(t)
	srv.priceProvider = nil

	seedDoc(srv, &Document{
		ID: "doc-5", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{priceableRow("row-1", "MPN-1")},
	})
	req := authedRequest(http.MethodPost, "/api/documents/doc-5/price", "", token)
	req.SetPathValue("id", "doc-5")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// If a previous pricing run flagged a row pricing_unavailable, a subsequent
// run that finds an offer must clear the flag — otherwise the warnings
// panel would keep nagging about a row that was successfully priced.
func TestPriceBOM_ClearsStaleUnavailableFlagOnSuccess(t *testing.T) {
	srv, provider, _, _, token := newPricingServer(t)
	provider.set("MPN-6", []SupplierOffer{{
		Supplier: "RS", SKU: "RS-6", Currency: "GBP", Source: "stub",
		PriceBreaks: []PriceBreak{{1, 0.99}},
		FetchedAt:   time.Now().UTC(),
	}})
	row := priceableRow("row-1", "MPN-6")
	row.Flags = []string{FlagPricingUnavailable, "unit_ambiguous"}
	seedDoc(srv, &Document{
		ID: "doc-6", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{row},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-6/price", "", token)
	req.SetPathValue("id", "doc-6")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	stored, _ := srv.store.get("doc-6")
	assert.NotContains(t, stored.BOMRows[0].Flags, FlagPricingUnavailable,
		"stale unavailable flag must clear once pricing succeeds")
	assert.Contains(t, stored.BOMRows[0].Flags, "unit_ambiguous",
		"unrelated flags must survive")
}

// Provider transport failures (network, 5xx) must propagate as a non-2xx
// response. Caching the failure as "no offers" would lock the row into an
// incorrect pricing_unavailable state for the full TTL.
func TestPriceBOM_ProviderErrorReturns502(t *testing.T) {
	srv, provider, _, _, token := newPricingServer(t)
	provider.err = errSimulated
	seedDoc(srv, &Document{
		ID: "doc-7", OrganizationID: "org-1", Filename: "x.pdf", Status: StatusDone,
		BOMRows: []BOMRow{priceableRow("row-1", "MPN-7")},
	})

	req := authedRequest(http.MethodPost, "/api/documents/doc-7/price", "", token)
	req.SetPathValue("id", "doc-7")
	w := httptest.NewRecorder()
	srv.priceBOM(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// Compile-time check that the in-memory fakes satisfy the real interfaces.
// (The handler tests rely on this implicitly; making it explicit catches
// interface drift early.)
var _ priceCacheRepository = (*memPriceCache)(nil)
var _ pricingRunRepository = (*memPricingRuns)(nil)
var _ pricingProvider = (*stubPricingProvider)(nil)
var _ pricingProvider = (*mockPricingProvider)(nil)

// errSimulated is a sentinel used by TestPriceBOM_ProviderErrorReturns502
// to force the stub into the error path.
var errSimulated = errSimulatedT{}

type errSimulatedT struct{}

func (errSimulatedT) Error() string { return "simulated transport failure" }

// Use context.Background to keep the linter satisfied even though tests
// above use it explicitly — touchpoint kept here so an unused-import sweep
// doesn't strip context.
var _ = context.Background