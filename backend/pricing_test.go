package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cache miss must return (nil, false) so the caller falls through to the
// upstream provider. A returned-but-empty slice would lie about coverage.
func TestPriceCache_LookupMissReturnsFalse(t *testing.T) {
	c := newMemPriceCache()
	offers, ok := c.get("MPN-UNKNOWN", "GBP", 24*time.Hour)
	assert.False(t, ok)
	assert.Nil(t, offers)
}

// Cache hit must return every offer for the (mpn, currency) tuple. Crucial
// invariant: a single MPN may have offers from multiple suppliers, and we
// must return all of them in one call so the row's Best-Price decoration
// has the full ladder to work with.
func TestPriceCache_LookupReturnsAllSuppliersForMPN(t *testing.T) {
	c := newMemPriceCache()
	now := time.Now().UTC()
	require.NoError(t, c.put("MPN-A", []SupplierOffer{
		{Supplier: "DigiKey", SKU: "DK-A", Currency: "GBP", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 2.00}, {100, 1.50}}, Source: "stub"},
		{Supplier: "Mouser", SKU: "MO-A", Currency: "GBP", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 2.10}}, Source: "stub"},
	}))

	offers, ok := c.get("MPN-A", "GBP", 24*time.Hour)
	require.True(t, ok)
	assert.Len(t, offers, 2)
}

// TTL is a critical correctness boundary — a stale row must look like a
// miss so the caller refreshes from upstream. Returning stale data here
// would defeat the entire reason for caching with a freshness window.
func TestPriceCache_StaleRowsTreatedAsMiss(t *testing.T) {
	c := newMemPriceCache()
	old := time.Now().UTC().Add(-48 * time.Hour) // older than the 24h TTL
	require.NoError(t, c.put("MPN-OLD", []SupplierOffer{
		{Supplier: "DigiKey", SKU: "DK-OLD", Currency: "GBP", FetchedAt: old,
			PriceBreaks: []PriceBreak{{1, 5.00}}, Source: "stub"},
	}))

	offers, ok := c.get("MPN-OLD", "GBP", 24*time.Hour)
	assert.False(t, ok, "rows older than the TTL must be treated as cache misses")
	assert.Nil(t, offers)
}

// Currency is part of the cache key. Two orgs on different currencies must
// not see each other's prices — and within one org, refetching after a
// currency change must hit the upstream, not return USD rows for a GBP query.
func TestPriceCache_CurrencyIsolatesEntries(t *testing.T) {
	c := newMemPriceCache()
	now := time.Now().UTC()
	require.NoError(t, c.put("MPN-X", []SupplierOffer{
		{Supplier: "DigiKey", SKU: "DK-X", Currency: "USD", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 2.50}}, Source: "stub"},
	}))

	_, ok := c.get("MPN-X", "GBP", 24*time.Hour)
	assert.False(t, ok, "USD entries must not satisfy a GBP query")
}

// MPN matching must be case-insensitive — the same drawing might write the
// MPN as "cf130.07.05.ul" while a CSV upload normalised it to the upper
// form. The cache must collapse them.
func TestPriceCache_MPNCaseInsensitive(t *testing.T) {
	c := newMemPriceCache()
	now := time.Now().UTC()
	require.NoError(t, c.put("mpn-mix", []SupplierOffer{
		{Supplier: "DigiKey", SKU: "DK-MIX", Currency: "GBP", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 1.0}}, Source: "stub"},
	}))

	_, ok := c.get("MPN-MIX", "GBP", 24*time.Hour)
	assert.True(t, ok, "lookup must be case-insensitive on the MPN key")
}

// pickBestUnitPrice picks the largest qty break ≤ requested qty. This is
// the rule SAP buyers expect when reading a quote: "if I order 75, what's
// my unit price?" → take the 50-break (or 25-break if no 50 exists), not
// the 100-break.
func TestPickBestUnitPrice_LargestBreakAtOrBelowQty(t *testing.T) {
	breaks := []PriceBreak{{1, 2.00}, {25, 1.50}, {100, 1.00}}

	cases := []struct {
		qty  int
		want float64
	}{
		{1, 2.00},
		{24, 2.00},   // below the 25-break
		{25, 1.50},   // exactly at the 25-break
		{75, 1.50},   // between 25 and 100
		{100, 1.00},  // exactly at the 100-break
		{1000, 1.00}, // above the largest break
	}
	for _, tc := range cases {
		p := pickBestUnitPrice(breaks, tc.qty)
		require.NotNil(t, p, "qty=%d", tc.qty)
		assert.InDelta(t, tc.want, *p, 1e-9, "qty=%d", tc.qty)
	}
}

// summariseOffers computes both best-price and best-stock supplier in one
// pass. They are not always the same supplier — the cheapest may be out of
// stock — so the UI surfaces both.
func TestSummariseOffers_BestPriceAndBestStockMayDiffer(t *testing.T) {
	stock := func(n int) *int { return &n }
	offers := []SupplierOffer{
		{Supplier: "DigiKey", Currency: "GBP", Stock: stock(50),
			PriceBreaks: []PriceBreak{{1, 1.00}}},
		{Supplier: "Mouser", Currency: "GBP", Stock: stock(500),
			PriceBreaks: []PriceBreak{{1, 1.20}}},
	}
	price, bestStockSup := summariseOffers(offers, 1)
	require.NotNil(t, price)
	assert.InDelta(t, 1.00, price.Amount, 1e-9, "best price is DigiKey @ £1.00")
	assert.Equal(t, "Mouser", bestStockSup, "best stock is Mouser with 500 units")
}

// summariseOffers must return (nil, "") for an empty offer list so the
// caller can treat it as "no pricing data available" without a special case.
func TestSummariseOffers_EmptyReturnsNilAndBlank(t *testing.T) {
	price, sup := summariseOffers(nil, 10)
	assert.Nil(t, price)
	assert.Equal(t, "", sup)
}

// Mock provider must return canned offers for known MPNs. This is the
// fixture set the local dev experience relies on when no Nexar credentials
// are available.
func TestMockPricingProvider_KnownMPN(t *testing.T) {
	p := newMockPricingProvider()
	offers, err := p.priceByMPN(context.Background(), "MOCK-MULTI", "GBP")
	require.NoError(t, err)
	assert.Len(t, offers, 2, "MOCK-MULTI has two supplier offers")
	for _, o := range offers {
		assert.Equal(t, "GBP", o.Currency)
		assert.Equal(t, "mock", o.Source)
		assert.NotEmpty(t, o.PriceBreaks)
	}
}

// Unknown MPNs return (nil, nil) — empty result, not error. The handler
// distinguishes "no offers" from "transport failure" by the error value.
func TestMockPricingProvider_UnknownMPNReturnsEmpty(t *testing.T) {
	p := newMockPricingProvider()
	offers, err := p.priceByMPN(context.Background(), "DOES-NOT-EXIST", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// A single supplier can return multiple offers for one MPN (TME EU vs TME UK,
// Arrow vs Verical-via-Arrow, etc., distinguished by SKU). The cache must
// preserve both — collapsing them by supplier alone silently drops data the
// operator needs to compare reels vs cut-lengths or regional warehouses.
//
// Regression guard against the original (mpn, supplier, currency) UNIQUE
// constraint that the live-Nexar test surfaced as too narrow.
func TestPriceCache_PreservesMultipleOffersFromSameSupplier(t *testing.T) {
	c := newMemPriceCache()
	now := time.Now().UTC()
	require.NoError(t, c.put("MPN-MULTI", []SupplierOffer{
		{Supplier: "TME", SKU: "TME-EU-123", Currency: "GBP", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 2.50}}, Source: "stub"},
		{Supplier: "TME", SKU: "TME-UK-456", Currency: "GBP", FetchedAt: now,
			PriceBreaks: []PriceBreak{{1, 3.10}}, Source: "stub"},
	}))

	offers, ok := c.get("MPN-MULTI", "GBP", 24*time.Hour)
	require.True(t, ok)
	assert.Len(t, offers, 2, "both TME warehouses must round-trip — overwriting by supplier loses one")

	skus := map[string]bool{}
	for _, o := range offers {
		skus[o.SKU] = true
	}
	assert.True(t, skus["TME-EU-123"] && skus["TME-UK-456"], "both SKUs must survive a write")
}

// normaliseSupplierName must merge brand-equivalent names (Premier Farnell's
// Farnell/Newark/element14 lineup, Arrow's Arrow/Verical pair, RS's various
// post-acquisition rebrands) so the UI shows one row per real vendor —
// not three rows for "the same supplier, different geos".
func TestNormaliseSupplierName_MergesBrandEquivalents(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Digi-Key", "DigiKey"},
		{"Digi-Key Electronics", "DigiKey"},
		{"Mouser Electronics", "Mouser"},
		{"Farnell", "Farnell"},
		{"element14", "Farnell"},
		{"element14 APAC", "Farnell"},
		{"Newark", "Farnell"},
		{"RS", "RS"},
		{"RS Components", "RS"},
		{"RS (Formerly Allied Electronics)", "RS"},
		{"Arrow", "Arrow"},
		{"Arrow Electronics", "Arrow"},
		{"Verical", "Arrow"},
		{"Future Electronics", "Future"},
		{"Avnet", "Avnet"},
		{"TME", "TME"},
		// Unknown names pass through trimmed.
		{"  Some Random Broker  ", "Some Random Broker"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, normaliseSupplierName(tc.in), "input=%q", tc.in)
	}
}
