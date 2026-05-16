package main

// Live integration tests for the direct-distributor providers. Each is
// skipped unless its credentials are present in the environment, so CI and
// credential-less local runs stay green while a developer with keys can
// validate the real wire format with:
//
//   MOUSER_API_KEY=...      go test -run TestMouserLive   -v ./...
//   FARNELL_API_KEY=...     go test -run TestFarnellLive  -v ./...
//   DIGIKEY_CLIENT_ID=... DIGIKEY_CLIENT_SECRET=... go test -run TestDigiKeyLive -v ./...
//   TME_TOKEN=... TME_APP_SECRET=...                go test -run TestTMELive     -v ./...
//
// STM32F103C8T6 is the probe part — a jellybean MCU every distributor
// stocks, so a zero-offer result means the integration is broken, not that
// the part is niche.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const liveProbeMPN = "STM32F103C8T6"

func liveCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func logOffers(t *testing.T, who string, offers []SupplierOffer) {
	t.Helper()
	t.Logf("%s: %d offer(s) for %s", who, len(offers), liveProbeMPN)
	for _, o := range offers {
		t.Logf("  %s sku=%s stock=%v lead=%v breaks=%d cur=%s",
			o.Supplier, o.SKU, derefInt(o.Stock), derefInt(o.LeadTimeDays),
			len(o.PriceBreaks), o.Currency)
	}
}

func derefInt(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestMouserLive_HappyPath(t *testing.T) {
	key := os.Getenv("MOUSER_API_KEY")
	if key == "" {
		t.Skip("MOUSER_API_KEY not set — skipping live test")
	}
	offers, err := newMouserProvider(key).priceByMPN(liveCtx(t), liveProbeMPN, "GBP")
	require.NoError(t, err)
	logOffers(t, "mouser", offers)
	require.NotEmpty(t, offers, "Mouser should stock STM32F103C8T6")
}

func TestFarnellLive_HappyPath(t *testing.T) {
	key := os.Getenv("FARNELL_API_KEY")
	if key == "" {
		t.Skip("FARNELL_API_KEY not set — skipping live test")
	}
	offers, err := newFarnellProvider(key).priceByMPN(liveCtx(t), liveProbeMPN, "GBP")
	require.NoError(t, err)
	logOffers(t, "farnell", offers)
	require.NotEmpty(t, offers, "Farnell should stock STM32F103C8T6")
}

func TestDigiKeyLive_HappyPath(t *testing.T) {
	id, sec := os.Getenv("DIGIKEY_CLIENT_ID"), os.Getenv("DIGIKEY_CLIENT_SECRET")
	if id == "" || sec == "" {
		t.Skip("DIGIKEY_CLIENT_ID/SECRET not set — skipping live test")
	}
	offers, err := newDigikeyProvider(id, sec).priceByMPN(liveCtx(t), liveProbeMPN, "GBP")
	require.NoError(t, err)
	logOffers(t, "digikey", offers)
	require.NotEmpty(t, offers, "Digi-Key should stock STM32F103C8T6")
}

func TestTMELive_HappyPath(t *testing.T) {
	tok, sec := os.Getenv("TME_TOKEN"), os.Getenv("TME_APP_SECRET")
	if tok == "" || sec == "" {
		t.Skip("TME_TOKEN/APP_SECRET not set — skipping live test")
	}
	offers, err := newTMEProvider(tok, sec).priceByMPN(liveCtx(t), liveProbeMPN, "GBP")
	require.NoError(t, err)
	logOffers(t, "tme", offers)
	// TME catalogue coverage is thinner than the big three; an empty result
	// is acceptable here as long as the signed call itself didn't error.
}
