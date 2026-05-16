package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProv struct {
	nm     string
	offers []SupplierOffer
	err    error
	calls  int32
	delay  time.Duration
}

func (f *fakeProv) name() string { return f.nm }

func (f *fakeProv) priceByMPN(ctx context.Context, _, _ string) ([]SupplierOffer, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.offers, f.err
}

func off(supplier, sku string, price float64) SupplierOffer {
	return SupplierOffer{
		Supplier: supplier, SKU: sku, Currency: "GBP", Source: supplier,
		PriceBreaks: []PriceBreak{{1, price}},
	}
}

// All providers contribute — the merged result is the union of every
// provider's offers. This is the whole point of going multi-source.
func TestMultiProvider_MergesAllOffers(t *testing.T) {
	a := &fakeProv{nm: "a", offers: []SupplierOffer{off("Mouser", "M1", 1.0)}}
	b := &fakeProv{nm: "b", offers: []SupplierOffer{off("Farnell", "F1", 1.1), off("DigiKey", "D1", 1.2)}}
	m := newMultiProvider(a, b)

	offers, err := m.priceByMPN(context.Background(), "X", "GBP")
	require.NoError(t, err)
	assert.Len(t, offers, 3)
	assert.Equal(t, int32(1), a.calls)
	assert.Equal(t, int32(1), b.calls)
}

// One provider down must NOT fail the whole pricing run — a single dead
// distributor API can't be allowed to blank out every other supplier's
// prices. We return the survivors with no error.
func TestMultiProvider_PartialFailureReturnsSurvivors(t *testing.T) {
	good := &fakeProv{nm: "good", offers: []SupplierOffer{off("Mouser", "M1", 2.0)}}
	bad := &fakeProv{nm: "bad", err: errors.New("upstream 500")}
	m := newMultiProvider(good, bad)

	offers, err := m.priceByMPN(context.Background(), "X", "GBP")
	require.NoError(t, err, "one failure among successes must not error the whole call")
	require.Len(t, offers, 1)
	assert.Equal(t, "Mouser", offers[0].Supplier)
}

// Only when EVERY provider errors do we surface an error (handler then
// 502s — there is genuinely no pricing path). The aggregated error should
// mention each failed provider for debuggability.
func TestMultiProvider_AllFailuresErrors(t *testing.T) {
	a := &fakeProv{nm: "a", err: errors.New("a down")}
	b := &fakeProv{nm: "b", err: errors.New("b down")}
	m := newMultiProvider(a, b)

	offers, err := m.priceByMPN(context.Background(), "X", "GBP")
	require.Error(t, err)
	assert.Nil(t, offers)
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
}

// All providers succeed but none has the part → (nil, nil): "no coverage",
// which the handler maps to the pricing_unavailable flag, not a 502.
func TestMultiProvider_AllEmptyReturnsNilNoError(t *testing.T) {
	a := &fakeProv{nm: "a"}
	b := &fakeProv{nm: "b"}
	m := newMultiProvider(a, b)

	offers, err := m.priceByMPN(context.Background(), "X", "GBP")
	assert.NoError(t, err)
	assert.Nil(t, offers)
}

// Distributor APIs overlap — two providers can report the same supplier's
// listing for the same SKU. Dedupe on (supplier, sku); the FIRST provider
// in declaration order wins, so callers can prioritise the more trusted
// source by ordering.
func TestMultiProvider_DedupesBySupplierSKU_FirstWins(t *testing.T) {
	direct := &fakeProv{nm: "direct", offers: []SupplierOffer{off("DigiKey", "D1", 1.00)}}
	aggregator := &fakeProv{nm: "agg", offers: []SupplierOffer{
		off("DigiKey", "D1", 1.50), // dup (supplier+sku) — must be dropped
		off("Arrow", "A1", 2.00),   // unique — kept
	}}
	m := newMultiProvider(direct, aggregator)

	offers, err := m.priceByMPN(context.Background(), "X", "GBP")
	require.NoError(t, err)
	require.Len(t, offers, 2)
	var dk *SupplierOffer
	for i := range offers {
		if offers[i].Supplier == "DigiKey" {
			dk = &offers[i]
		}
	}
	require.NotNil(t, dk)
	assert.InDelta(t, 1.00, dk.PriceBreaks[0].Price, 1e-9, "first provider's DigiKey offer must win the dedupe")
}

// Providers run concurrently, not serially — three 40ms providers should
// finish in well under the 120ms a serial fan-out would take.
func TestMultiProvider_RunsConcurrently(t *testing.T) {
	d := 40 * time.Millisecond
	a := &fakeProv{nm: "a", delay: d, offers: []SupplierOffer{off("Mouser", "M", 1)}}
	b := &fakeProv{nm: "b", delay: d, offers: []SupplierOffer{off("Farnell", "F", 1)}}
	c := &fakeProv{nm: "c", delay: d, offers: []SupplierOffer{off("TME", "T", 1)}}
	m := newMultiProvider(a, b, c)

	start := time.Now()
	_, err := m.priceByMPN(context.Background(), "X", "GBP")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond, "fan-out must be concurrent (3×40ms serial would be ~120ms)")
}

// name() should reflect the composition so startup logs and the run
// summary make the active source set obvious.
func TestMultiProvider_NameListsChildren(t *testing.T) {
	m := newMultiProvider(&fakeProv{nm: "mouser"}, &fakeProv{nm: "farnell"})
	assert.Equal(t, "multi(mouser+farnell)", m.name())
}

var _ pricingProvider = (*multiProvider)(nil)
