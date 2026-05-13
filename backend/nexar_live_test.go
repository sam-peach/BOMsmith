package main

// nexar_live_test.go — live integration tests for the Nexar provider.
//
// Skipped in CI and in any local run that doesn't have NEXAR_CLIENT_ID +
// NEXAR_CLIENT_SECRET in the environment. The other Nexar tests (in
// nexar_test.go) use httptest fixtures and don't burn the real API quota.
//
// Run locally with:
//
//   NEXAR_CLIENT_ID=... NEXAR_CLIENT_SECRET=... \
//   go test -run TestNexarLive -v ./...
//
// Each test makes ONE real API call (one token + one GraphQL request),
// so the cost is bounded.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newLiveNexarProviderOrSkip(t *testing.T) *nexarProvider {
	t.Helper()
	id := os.Getenv("NEXAR_CLIENT_ID")
	secret := os.Getenv("NEXAR_CLIENT_SECRET")
	if id == "" || secret == "" {
		t.Skip("NEXAR_CLIENT_ID + NEXAR_CLIENT_SECRET not set — skipping live test")
	}
	return newNexarProvider(id, secret)
}

// Verifies the full happy path against real Nexar: OAuth token exchange,
// GraphQL query, response parsing into SupplierOffer. Uses CF130.07.05.UL
// (a Lapp tri-rated switchgear wire) — the same MPN that surfaced in the
// mapping-search audit, so we know Octopart carries it.
func TestNexarLive_HappyPath(t *testing.T) {
	p := newLiveNexarProviderOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	offers, err := p.priceByMPN(ctx, "CF130.07.05.UL", "GBP")
	require.NoError(t, err, "live Nexar call failed")

	if len(offers) == 0 {
		t.Logf("warning: CF130.07.05.UL returned zero offers — try a different MPN")
	}
	for i, o := range offers {
		t.Logf("offer[%d]: supplier=%s sku=%s stock=%v lead=%v breaks=%d url=%s",
			i, o.Supplier, o.SKU, derefInt(o.Stock), derefInt(o.LeadTimeDays),
			len(o.PriceBreaks), o.SupplierURL)
		for _, b := range o.PriceBreaks {
			t.Logf("  qty=%d price=%.4f %s", b.Quantity, b.Price, o.Currency)
		}
		require.Equal(t, "nexar", o.Source, "Source must be tagged nexar")
		require.Equal(t, "GBP", o.Currency)
		require.NotEmpty(t, o.Supplier)
	}
}

// Smoke-test a widely-stocked MCU as a fallback — useful if the wire MPN
// has narrowed distribution in Octopart's index and the first test prints
// "zero offers". Run independently:
//   go test -run TestNexarLive_PopularPart -v
func TestNexarLive_PopularPart(t *testing.T) {
	p := newLiveNexarProviderOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	offers, err := p.priceByMPN(ctx, "STM32F103C8T6", "GBP")
	require.NoError(t, err, "live Nexar call failed")
	require.NotEmpty(t, offers, "STM32F103C8T6 should be carried by multiple authorised distributors")
	t.Logf("STM32F103C8T6: %d offers across suppliers", len(offers))
	for _, o := range offers {
		t.Logf("  %s · sku=%s · %d price breaks · stock=%v",
			o.Supplier, o.SKU, len(o.PriceBreaks), derefInt(o.Stock))
	}
}

func derefInt(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}
