package main

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// POST /api/documents/{id}/price — runs pricing for every BOM row with a
// non-empty MPN. Orchestrates: cache lookup → provider call on miss →
// cache UPSERT → per-row flag → persisted pricing run.
//
// Cache hits don't call the provider (the whole point of caching). Provider
// transport failures bubble as 502 — we deliberately do NOT cache "no offers"
// when the upstream errored out, because that would lock the MPN into a
// pricing_unavailable state for the full TTL.
func (s *server) priceBOM(w http.ResponseWriter, r *http.Request) {
	if s.priceProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "pricing provider not configured")
		return
	}
	id := r.PathValue("id")
	doc, err := s.store.get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	sd := sessionFromContext(r)

	currency := s.pricingCurrency
	if currency == "" {
		currency = defaultPricingCurrency
	}
	ttl := s.pricingCacheTTL
	if ttl <= 0 {
		ttl = defaultPricingCacheTTL
	}

	run := &PricingRun{
		DocumentID:     doc.ID,
		OrganizationID: sd.OrgID,
		StartedAt:      time.Now().UTC(),
		Currency:       currency,
		RowsTotal:      len(doc.BOMRows),
	}
	if err := s.pricingRuns.create(run); err != nil {
		log.Printf("pricing: create run for %s: %v", doc.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to create pricing run")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	for i := range doc.BOMRows {
		row := &doc.BOMRows[i]
		mpn := strings.TrimSpace(row.ManufacturerPartNumber)
		if mpn == "" {
			run.RowsSkipped++
			continue
		}

		// Cache lookup first — the whole point of the cache.
		offers, hit := s.priceCache.get(mpn, currency, ttl)
		if hit {
			run.CacheHits++
		} else {
			fetched, err := s.priceProvider.priceByMPN(ctx, mpn, currency)
			if err != nil {
				log.Printf("pricing: provider error for %q on doc %s: %v", mpn, doc.ID, err)
				run.ErrorMessage = err.Error()
				now := time.Now().UTC()
				run.CompletedAt = &now
				_ = s.pricingRuns.complete(run)
				writeError(w, http.StatusBadGateway, "pricing provider error")
				return
			}
			run.ProviderCallsMade++
			if len(fetched) > 0 {
				if err := s.priceCache.put(mpn, fetched); err != nil {
					log.Printf("pricing: cache put for %q on doc %s: %v", mpn, doc.ID, err)
				}
			}
			offers = fetched
		}

		// Apply the per-row outcome to Flags. The flag tracks the most
		// recent run's result: success clears it, no-offers sets it.
		if len(offers) == 0 {
			run.RowsUnavailable++
			row.Flags = ensureFlag(row.Flags, FlagPricingUnavailable)
		} else {
			run.RowsPriced++
			row.Flags = removeFlag(row.Flags, FlagPricingUnavailable)
		}
	}

	now := time.Now().UTC()
	run.CompletedAt = &now
	if err := s.pricingRuns.complete(run); err != nil {
		log.Printf("pricing: complete run %s: %v", run.ID, err)
	}

	s.store.save(doc)

	// Decorate the response with joined pricing so the frontend renders
	// immediately rather than needing a second GET.
	decoratePricing(doc, s.priceCache, currency, ttl)
	doc.LastPricingRun = run

	writeJSON(w, http.StatusOK, doc)
}

// decoratePricing fills BOMRow.Pricing for every row by joining the cache.
// Called by both POST /price (after the run) and GET /documents/{id} so the
// frontend never has to derive pricing from a separate endpoint.
func decoratePricing(doc *Document, cache priceCacheRepository, currency string, ttl time.Duration) {
	if doc == nil || cache == nil {
		return
	}
	for i := range doc.BOMRows {
		row := &doc.BOMRows[i]
		mpn := strings.TrimSpace(row.ManufacturerPartNumber)
		if mpn == "" {
			continue
		}
		offers, ok := cache.get(mpn, currency, ttl)
		if !ok {
			continue
		}
		qty := 1
		if row.Quantity.Value != nil && *row.Quantity.Value > 0 {
			qty = max(int(*row.Quantity.Value), 1)
		}
		bestPrice, bestStockSupplier := summariseOffers(offers, qty)
		// FetchedAt = the most recent FetchedAt across this MPN's offers.
		var fetchedAt time.Time
		for _, o := range offers {
			if o.FetchedAt.After(fetchedAt) {
				fetchedAt = o.FetchedAt
			}
		}
		row.Pricing = &RowPricing{
			Offers:            offers,
			BestUnitPrice:     bestPrice,
			BestStockSupplier: bestStockSupplier,
			FetchedAt:         fetchedAt,
		}
	}
}

// ensureFlag appends s if not already present.
func ensureFlag(flags []string, s string) []string {
	if slices.Contains(flags, s) {
		return flags
	}
	return append(flags, s)
}

// removeFlag removes every occurrence of s.
func removeFlag(flags []string, s string) []string {
	out := flags[:0]
	for _, f := range flags {
		if f != s {
			out = append(out, f)
		}
	}
	// Defensive copy so callers don't observe shared backing array tricks.
	if out == nil {
		return []string{}
	}
	result := make([]string, len(out))
	copy(result, out)
	return result
}

