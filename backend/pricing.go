package main

// Pricing layer — Phase 1 of docs/pricing.md.
//
// Flow on POST /api/documents/{id}/price:
//   1. For each BOMRow with a confirmed, non-empty MPN:
//      a. Check priceCacheRepository.get(mpn, currency, ttl).
//      b. On miss, call pricingProvider.priceByMPN(mpn, currency).
//      c. UPSERT the returned offers into the cache.
//      d. If the result is still empty, set the pricing_unavailable flag
//         on the row.
//   2. Persist a pricingRun summarising the outcome.
//   3. Decorate the response Document with the joined pricing per row.
//
// Pricing is intentionally NOT org-scoped — two orgs querying the same MPN
// share the cache. The mapping (CPN→IPN) remains org-scoped via the
// mappings table; pricing is a downstream commodity lookup.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// defaultPricingCacheTTL is the freshness window applied to part_prices rows
// when the operator triggers a pricing run. Overridable via PRICING_CACHE_TTL_HOURS.
const defaultPricingCacheTTL = 24 * time.Hour

// defaultPricingCurrency is the currency used when no org-level override is
// set. UK-based ops; matches the existing Settings page default.
const defaultPricingCurrency = "GBP"

// ── interfaces ────────────────────────────────────────────────────────────────

// pricingProvider fetches live supplier offers from an upstream source.
// Implementations: nexarProvider (production), mockPricingProvider (local dev).
//
// Empty result is NOT an error — it's a normal "this MPN is outside our
// coverage" answer. Callers distinguish via the returned slice length.
type pricingProvider interface {
	priceByMPN(ctx context.Context, mpn, currency string) ([]SupplierOffer, error)
	// name returns a short identifier used for the offer.Source field and logs.
	name() string
}

// priceCacheRepository wraps the part_prices table. Cache entries are global
// (not org-scoped) by design — pricing is commodity data.
type priceCacheRepository interface {
	// get returns cached offers younger than ttl. If the cache is cold or all
	// rows for (mpn, currency) are older than ttl, returns (nil, false).
	get(mpn, currency string, ttl time.Duration) ([]SupplierOffer, bool)
	// put UPSERTs each offer keyed by (mpn, supplier, currency). Stale rows
	// for the same key are overwritten in place.
	put(mpn string, offers []SupplierOffer) error
}

// pricingRunRepository wraps the pricing_runs table.
type pricingRunRepository interface {
	create(run *PricingRun) error
	complete(run *PricingRun) error
	// latest returns the most recent run for a document, or (nil, nil) if none.
	latest(documentID string) (*PricingRun, error)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// normMPN normalises an MPN for case-insensitive cache keying.
func normMPN(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// pickBestUnitPrice walks an offer's price-break ladder and returns the price
// at the largest break ≤ qty. Returns nil if the ladder is empty.
func pickBestUnitPrice(breaks []PriceBreak, qty int) *float64 {
	if len(breaks) == 0 {
		return nil
	}
	if qty < 1 {
		qty = 1
	}
	var best *PriceBreak
	for i := range breaks {
		b := &breaks[i]
		if b.Quantity > qty {
			continue
		}
		if best == nil || b.Quantity > best.Quantity {
			best = b
		}
	}
	if best == nil {
		// All breaks require more than qty — return the smallest-qty price.
		best = &breaks[0]
		for i := range breaks {
			if breaks[i].Quantity < best.Quantity {
				best = &breaks[i]
			}
		}
	}
	v := best.Price
	return &v
}

// summariseOffers computes the per-row best price / best stock supplier from
// the offer list. qty is the operator-confirmed quantity from the BOMRow.
func summariseOffers(offers []SupplierOffer, qty int) (*Money, string) {
	if len(offers) == 0 {
		return nil, ""
	}
	var (
		bestPrice    *float64
		bestCurrency string
		bestStock    int
		bestStockSup string
	)
	for _, o := range offers {
		p := pickBestUnitPrice(o.PriceBreaks, qty)
		if p != nil && (bestPrice == nil || *p < *bestPrice) {
			bestPrice = p
			bestCurrency = o.Currency
		}
		if o.Stock != nil && *o.Stock > bestStock {
			bestStock = *o.Stock
			bestStockSup = o.Supplier
		}
	}
	var money *Money
	if bestPrice != nil {
		money = &Money{Amount: *bestPrice, Currency: bestCurrency}
	}
	return money, bestStockSup
}

// ── mockPricingProvider ───────────────────────────────────────────────────────

// mockPricingProvider returns canned offers for development and tests.
// Selected via PRICING_PROVIDER=mock; lets the full UX run with no Nexar
// credentials. The fixture set is intentionally tiny — three MPNs covering
// the common cases (multiple suppliers with breaks, single supplier, unknown).
type mockPricingProvider struct {
	now func() time.Time
}

func newMockPricingProvider() *mockPricingProvider {
	return &mockPricingProvider{now: func() time.Time { return time.Now().UTC() }}
}

func (m *mockPricingProvider) name() string { return "mock" }

func (m *mockPricingProvider) priceByMPN(_ context.Context, mpn, currency string) ([]SupplierOffer, error) {
	now := m.now()
	stock := func(n int) *int { return &n }
	lead := func(n int) *int { return &n }
	switch normMPN(mpn) {
	case "MOCK-MULTI":
		// Multiple suppliers, full break ladder. Best price at qty=100 is Mouser.
		return []SupplierOffer{
			{
				Supplier: "DigiKey", SKU: "DK-MULTI-100",
				PriceBreaks: []PriceBreak{{1, 2.34}, {10, 2.05}, {100, 1.78}, {1000, 1.42}},
				Stock:       stock(4200), LeadTimeDays: lead(14),
				SupplierURL: "https://example.com/digikey/DK-MULTI-100",
				Source:      "mock", Currency: currency, FetchedAt: now,
			},
			{
				Supplier: "Mouser", SKU: "MO-MULTI-100",
				PriceBreaks: []PriceBreak{{1, 2.40}, {10, 2.10}, {100, 1.65}, {1000, 1.35}},
				Stock:       stock(1800), LeadTimeDays: lead(7),
				SupplierURL: "https://example.com/mouser/MO-MULTI-100",
				Source:      "mock", Currency: currency, FetchedAt: now,
			},
		}, nil
	case "MOCK-SINGLE":
		return []SupplierOffer{
			{
				Supplier: "Farnell", SKU: "F-SINGLE-1",
				PriceBreaks: []PriceBreak{{1, 0.85}, {25, 0.62}},
				Stock:       stock(330), LeadTimeDays: lead(3),
				SupplierURL: "https://example.com/farnell/F-SINGLE-1",
				Source:      "mock", Currency: currency, FetchedAt: now,
			},
		}, nil
	default:
		return nil, nil
	}
}

// ── pgPriceCacheRepository ────────────────────────────────────────────────────

type pgPriceCacheRepository struct {
	db  *sql.DB
	now func() time.Time
}

func (r *pgPriceCacheRepository) timeNow() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now().UTC()
}

func (r *pgPriceCacheRepository) get(mpn, currency string, ttl time.Duration) ([]SupplierOffer, bool) {
	rows, err := r.db.Query(`
		SELECT supplier, sku, price_breaks, stock, lead_time_days, supplier_url,
		       source, currency, fetched_at
		FROM part_prices
		WHERE mpn = $1 AND currency = $2 AND fetched_at > $3`,
		normMPN(mpn), currency, r.timeNow().Add(-ttl),
	)
	if err != nil {
		log.Printf("priceCache get error: %v", err)
		return nil, false
	}
	defer rows.Close()
	var out []SupplierOffer
	for rows.Next() {
		var (
			o            SupplierOffer
			breaksJSON   []byte
			stock        sql.NullInt64
			leadTimeDays sql.NullInt64
		)
		if err := rows.Scan(&o.Supplier, &o.SKU, &breaksJSON, &stock, &leadTimeDays,
			&o.SupplierURL, &o.Source, &o.Currency, &o.FetchedAt); err != nil {
			log.Printf("priceCache get scan: %v", err)
			continue
		}
		if err := json.Unmarshal(breaksJSON, &o.PriceBreaks); err != nil {
			log.Printf("priceCache get unmarshal breaks: %v", err)
			continue
		}
		if stock.Valid {
			v := int(stock.Int64)
			o.Stock = &v
		}
		if leadTimeDays.Valid {
			v := int(leadTimeDays.Int64)
			o.LeadTimeDays = &v
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func (r *pgPriceCacheRepository) put(mpn string, offers []SupplierOffer) error {
	key := normMPN(mpn)
	if key == "" {
		return fmt.Errorf("priceCache put: mpn is required")
	}
	for _, o := range offers {
		breaksJSON, err := json.Marshal(o.PriceBreaks)
		if err != nil {
			return fmt.Errorf("priceCache put: marshal breaks: %w", err)
		}
		var stock, lead any
		if o.Stock != nil {
			stock = *o.Stock
		}
		if o.LeadTimeDays != nil {
			lead = *o.LeadTimeDays
		}
		fetchedAt := o.FetchedAt
		if fetchedAt.IsZero() {
			fetchedAt = r.timeNow()
		}
		_, err = r.db.Exec(`
			INSERT INTO part_prices
				(mpn, supplier, sku, currency, price_breaks, stock, lead_time_days,
				 supplier_url, source, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (mpn, supplier, sku, currency) DO UPDATE SET
				price_breaks   = EXCLUDED.price_breaks,
				stock          = EXCLUDED.stock,
				lead_time_days = EXCLUDED.lead_time_days,
				supplier_url   = EXCLUDED.supplier_url,
				source         = EXCLUDED.source,
				fetched_at     = EXCLUDED.fetched_at`,
			key, o.Supplier, o.SKU, o.Currency, string(breaksJSON), stock, lead,
			o.SupplierURL, o.Source, fetchedAt,
		)
		if err != nil {
			return fmt.Errorf("priceCache put: upsert: %w", err)
		}
	}
	return nil
}

// ── pgPricingRunRepository ────────────────────────────────────────────────────

type pgPricingRunRepository struct {
	db *sql.DB
}

func (r *pgPricingRunRepository) create(run *PricingRun) error {
	return r.db.QueryRow(`
		INSERT INTO pricing_runs (document_id, organization_id, started_at, currency, rows_total)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		run.DocumentID, run.OrganizationID, run.StartedAt, run.Currency, run.RowsTotal,
	).Scan(&run.ID)
}

func (r *pgPricingRunRepository) complete(run *PricingRun) error {
	var errMsg any
	if run.ErrorMessage != "" {
		errMsg = run.ErrorMessage
	}
	_, err := r.db.Exec(`
		UPDATE pricing_runs SET
			completed_at      = $2,
			rows_priced       = $3,
			rows_unavailable  = $4,
			rows_skipped      = $5,
			nexar_calls_made  = $6,
			cache_hits        = $7,
			error_message     = $8
		WHERE id = $1`,
		run.ID, run.CompletedAt, run.RowsPriced, run.RowsUnavailable, run.RowsSkipped,
		run.NexarCallsMade, run.CacheHits, errMsg,
	)
	return err
}

func (r *pgPricingRunRepository) latest(documentID string) (*PricingRun, error) {
	var (
		run          PricingRun
		completedAt  sql.NullTime
		errorMessage sql.NullString
	)
	err := r.db.QueryRow(`
		SELECT id, document_id, organization_id, started_at, completed_at,
		       rows_total, rows_priced, rows_unavailable, rows_skipped,
		       nexar_calls_made, cache_hits, currency, error_message
		FROM pricing_runs
		WHERE document_id = $1
		ORDER BY started_at DESC
		LIMIT 1`,
		documentID,
	).Scan(&run.ID, &run.DocumentID, &run.OrganizationID, &run.StartedAt, &completedAt,
		&run.RowsTotal, &run.RowsPriced, &run.RowsUnavailable, &run.RowsSkipped,
		&run.NexarCallsMade, &run.CacheHits, &run.Currency, &errorMessage)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pricingRuns latest: %w", err)
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		run.ErrorMessage = errorMessage.String
	}
	return &run, nil
}

// listOffersForDocument loads cached offers for every MPN that appears on any
// BOMRow of the document. Used by GET /api/documents/{id} to decorate the
// response. We pull all currencies in one query and bucket client-side; the
// caller filters to the org's currency.
func listOffersForDocument(db *sql.DB, mpns []string) (map[string][]SupplierOffer, error) {
	if len(mpns) == 0 {
		return map[string][]SupplierOffer{}, nil
	}
	// Normalise + de-dupe the MPN list before the IN clause.
	seen := make(map[string]struct{}, len(mpns))
	args := make([]any, 0, len(mpns))
	placeholders := make([]string, 0, len(mpns))
	for _, raw := range mpns {
		k := normMPN(raw)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		args = append(args, k)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	if len(args) == 0 {
		return map[string][]SupplierOffer{}, nil
	}
	sqlText := `
		SELECT mpn, supplier, sku, price_breaks, stock, lead_time_days, supplier_url,
		       source, currency, fetched_at
		FROM part_prices
		WHERE mpn IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("listOffersForDocument: %w", err)
	}
	defer rows.Close()
	out := map[string][]SupplierOffer{}
	for rows.Next() {
		var (
			mpn          string
			o            SupplierOffer
			breaksJSON   []byte
			stock        sql.NullInt64
			leadTimeDays sql.NullInt64
		)
		if err := rows.Scan(&mpn, &o.Supplier, &o.SKU, &breaksJSON, &stock, &leadTimeDays,
			&o.SupplierURL, &o.Source, &o.Currency, &o.FetchedAt); err != nil {
			log.Printf("listOffersForDocument scan: %v", err)
			continue
		}
		if err := json.Unmarshal(breaksJSON, &o.PriceBreaks); err != nil {
			log.Printf("listOffersForDocument unmarshal: %v", err)
			continue
		}
		if stock.Valid {
			v := int(stock.Int64)
			o.Stock = &v
		}
		if leadTimeDays.Valid {
			v := int(leadTimeDays.Int64)
			o.LeadTimeDays = &v
		}
		out[mpn] = append(out[mpn], o)
	}
	return out, nil
}
