package main

// multiProvider fans a single priceByMPN out across several upstream
// providers concurrently and merges their offers. It is itself a
// pricingProvider, so the handler/cache/flag machinery is unchanged —
// "use Mouser + Farnell + DigiKey + TME together" is just one more
// provider from the orchestrator's point of view.
//
// Failure policy:
//   - at least one child returns without error  → return the merged
//     survivors, nil error (one dead distributor API must never blank out
//     every other supplier's prices for the whole BOM);
//   - every child errors                         → return nil + an
//     aggregated error (the handler 502s — there is no pricing path);
//   - all children succeed but none has the part → (nil, nil) → the row
//     gets the pricing_unavailable flag, same as the single-provider case.
//
// Dedupe: distributor APIs can overlap — two providers may report the
// same supplier+SKU. We keep the FIRST occurrence in provider declaration
// order, so ordering encodes source precedence.

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type multiProvider struct {
	providers []pricingProvider
}

func newMultiProvider(providers ...pricingProvider) *multiProvider {
	return &multiProvider{providers: providers}
}

func (m *multiProvider) name() string {
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.name()
	}
	return "multi(" + strings.Join(names, "+") + ")"
}

type provResult struct {
	idx    int
	offers []SupplierOffer
	err    error
}

func (m *multiProvider) priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error) {
	if len(m.providers) == 0 {
		return nil, nil
	}

	results := make([]provResult, len(m.providers))
	var wg sync.WaitGroup
	for i, p := range m.providers {
		wg.Add(1)
		go func(i int, p pricingProvider) {
			defer wg.Done()
			offers, err := p.priceByMPN(ctx, mpn, currency)
			results[i] = provResult{idx: i, offers: offers, err: err}
		}(i, p)
	}
	wg.Wait()

	var (
		merged    []SupplierOffer
		seen      = map[string]struct{}{}
		failures  []string
		anyOK     bool
	)
	// Iterate in provider declaration order so first-wins dedupe is
	// deterministic regardless of goroutine completion order.
	for i := range results {
		r := results[i]
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", m.providers[i].name(), r.err))
			continue
		}
		anyOK = true
		for _, o := range r.offers {
			key := strings.ToUpper(o.Supplier) + "|" + strings.ToUpper(o.SKU)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, o)
		}
	}

	if !anyOK {
		return nil, fmt.Errorf("all pricing providers failed: %s", strings.Join(failures, "; "))
	}
	return merged, nil
}
