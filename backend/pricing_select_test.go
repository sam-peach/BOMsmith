package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envMap turns a map into the env-lookup func selectPricingProvider takes,
// so these tests never touch real process env.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// No credentials and no explicit mode → mock, so a fresh `go run .` works
// with zero config.
func TestSelectProvider_NoCredsDefaultsToMock(t *testing.T) {
	p := selectPricingProvider(envMap(nil))
	require.NotNil(t, p)
	assert.Equal(t, "mock", p.name())
}

// A single available provider in auto mode is returned directly — no
// pointless multi(x) wrapper around one child.
func TestSelectProvider_SingleCredReturnsThatProviderUnwrapped(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"MOUSER_API_KEY": "mk",
	}))
	assert.Equal(t, "mouser", p.name())
}

// Several creds present in auto mode → a multiProvider composing them in
// the fixed Mouser→Farnell→DigiKey→TME order, so the first-wins dedupe is
// deterministic regardless of goroutine completion order.
func TestSelectProvider_AutoComposesInFixedOrder(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"MOUSER_API_KEY":        "mk",
		"FARNELL_API_KEY":       "fk",
		"DIGIKEY_CLIENT_ID":     "di",
		"DIGIKEY_CLIENT_SECRET": "ds",
		"TME_TOKEN":             "tt",
		"TME_APP_SECRET":        "ts",
	}))
	assert.Equal(t, "multi(mouser+farnell+digikey+tme)", p.name(),
		"composition order must be fixed so first-wins dedupe is deterministic")
}

// Partial creds compose only what's actually configured.
func TestSelectProvider_AutoComposesOnlyConfigured(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"FARNELL_API_KEY":       "fk",
		"DIGIKEY_CLIENT_ID":     "di",
		"DIGIKEY_CLIENT_SECRET": "ds",
	}))
	assert.Equal(t, "multi(farnell+digikey)", p.name())
}

// Incomplete OAuth pairs don't half-build a provider — Digi-Key needs both
// id AND secret; with only one it must be skipped, not panic or 401-loop.
func TestSelectProvider_IncompleteOAuthPairIgnored(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"DIGIKEY_CLIENT_ID": "di", // secret missing
		"MOUSER_API_KEY":    "mk",
	}))
	assert.Equal(t, "mouser", p.name(), "Digi-Key with no secret must be dropped, leaving only Mouser")
}

// Explicit single-source override pins one provider for cost/debug control
// even when other creds are present.
func TestSelectProvider_ExplicitSingleSourceOverride(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"PRICING_PROVIDER":      "farnell",
		"FARNELL_API_KEY":       "fk",
		"MOUSER_API_KEY":        "mk", // present but must be ignored
		"DIGIKEY_CLIENT_ID":     "di",
		"DIGIKEY_CLIENT_SECRET": "ds",
	}))
	assert.Equal(t, "farnell", p.name())
}

// An explicit provider whose creds are missing falls back to mock (with a
// log) rather than returning a provider that 401s every call.
func TestSelectProvider_ExplicitButMissingCredsFallsBackToMock(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{
		"PRICING_PROVIDER": "digikey", // no DIGIKEY_* creds
	}))
	assert.Equal(t, "mock", p.name())
}

// mock and csv-only are honoured verbatim regardless of creds present.
func TestSelectProvider_ModeMockAndCSVOnly(t *testing.T) {
	mock := selectPricingProvider(envMap(map[string]string{
		"PRICING_PROVIDER": "mock",
		"MOUSER_API_KEY":   "mk",
	}))
	assert.Equal(t, "mock", mock.name())

	csv := selectPricingProvider(envMap(map[string]string{"PRICING_PROVIDER": "csv-only"}))
	assert.Nil(t, csv, "csv-only → nil provider so /price 503s until the CSV path exists")
}

// "multi" behaves like auto-compose; with no creds it degrades to mock
// rather than an empty multi that always 502s.
func TestSelectProvider_ModeMultiWithNoCredsDegradesToMock(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{"PRICING_PROVIDER": "multi"}))
	assert.Equal(t, "mock", p.name())
}

// Unknown mode is a config typo — fail safe to mock, don't crash the
// server on boot.
func TestSelectProvider_UnknownModeFallsBackToMock(t *testing.T) {
	p := selectPricingProvider(envMap(map[string]string{"PRICING_PROVIDER": "octopart"}))
	assert.Equal(t, "mock", p.name())
}
