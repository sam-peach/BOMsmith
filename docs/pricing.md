# BOM Pricing — Design Doc

**Status:** Implemented. **Nexar/Octopart was removed** — see banner below.
**Last updated:** 2026-05-16
**Author:** sam-peach + Claude

> **⚠️ Current state (read first).** This doc is kept as the design *record*,
> including the original Nexar-first plan and the rationale that led to the
> provider abstraction. **Nexar/Octopart is no longer used.** Its free tier
> capped at 10 distinct parts and the paid tier required a business email +
> recurring spend, so it was replaced by home-grown direct-distributor
> providers — **Mouser, Farnell/element14, Digi-Key, TME** — composed by a
> `multiProvider` behind the same `pricingProvider` interface. Sections
> below that describe the Nexar GraphQL integration are **historical**; for
> the live spec see the env-var table (§10) and `docs/walkthrough.md` §6
> "Pricing providers". Mentions of Nexar elsewhere explain the journey, not
> the current system.

This document specifies a "Price BOM" step that runs *after* the operator has completed mapping the BOM. It pulls supplier pricing, stock, and lead-time data for every line, surfaces a best-price recommendation and full breakdown per row, and falls back gracefully when no data is available.

---

## Table of Contents

1. [Goals & non-goals](#1-goals--non-goals)
2. [User flow](#2-user-flow)
3. [Data model additions](#3-data-model-additions)
4. [Nexar / Octopart integration](#4-nexar--octopart-integration)
5. [Caching contract](#5-caching-contract)
6. [Fallback CSV format](#6-fallback-csv-format)
7. [Flag handling](#7-flag-handling)
8. [HTTP API surface](#8-http-api-surface)
9. [Frontend architecture](#9-frontend-architecture)
10. [Environment variables](#10-environment-variables)
11. [Open questions](#11-open-questions)
12. [Phased rollout](#12-phased-rollout)

---

## 1. Goals & non-goals

### Goals

- **Single explicit step** — operator clicks "Price BOM" once mappings are confirmed; pricing is never fetched implicitly during analysis or save.
- **Maximum detail per row** — best price, best-stock supplier, all available suppliers, qty 1/10/100/1000 price breaks, stock level, factory lead time.
- **Cross-supplier coverage** via Nexar GraphQL — one API covers DigiKey, Mouser, Farnell, RS, Newark, etc.
- **Hybrid fallback** — Nexar primary; Andrew's existing RS/Farnell CSV exports fill the long tail; rows with neither show `"No price available"` plus quick-links to supplier search pages.
- **Cache-first** — 24h DB-backed cache keyed by `(mpn, currency)` to keep API spend down and absorb Nexar outages.
- **Existing flag system** — `pricing_unavailable` slots into the per-row `Flags` array the same way `unit_ambiguous` does today.

### Non-goals (for v1)

- Real-time pricing / sub-day cache TTLs.
- Multi-currency display within one BOM (currency is org-level).
- Quoting workflows (placing orders, generating POs).
- Lifecycle / obsolescence data (Nexar has it; we don't need it yet).
- Pricing for non-MPN'd lines (the operator must have confirmed an MPN; lines without one are skipped and flagged).

---

## 2. User flow

```
┌──────────────────────────────────────────────────────────────┐
│  Workflow page  (existing)                                   │
│                                                              │
│  [BomTable — operator confirms CPNs, IPNs, MPNs]             │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  Toolbar:  [Export CSV]   [Price BOM ▶]              │    │
│  └──────────────────────────────────────────────────────┘    │
└──────────────────────────┬───────────────────────────────────┘
                           │  click "Price BOM"
                           ▼
                  POST /api/documents/{id}/price
                           │
                           ▼
            ┌──────────────────────────────┐
            │ for each row with confirmed  │
            │ MPN:                          │
            │   • check cache               │
            │   • if miss → Nexar           │
            │   • if Nexar empty → CSV      │
            │   • if CSV empty → flag       │
            └──────────────┬───────────────┘
                           │
                           ▼
              PricingRun + persisted offers
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Workflow page reloads with pricing data                    │
│                                                             │
│  • BomTable shows new "Best Price" column                   │
│  • Click a row → inline expansion: full supplier × qty grid │
│  • Footer: "BOM priced 2026-05-13 14:22 · 42/45 lines       │
│            priced · total @ qty 1: £482.30"                 │
│  • Rows without pricing: "No price available · [RS]         │
│    [Farnell] [Octopart] [Google]" quick-links               │
└─────────────────────────────────────────────────────────────┘
```

**Re-pricing**: clicking "Price BOM" again issues a fresh run. The cache layer absorbs the per-row cost — most rows return from cache, only stale rows hit Nexar.

**No partial UI updates during pricing**: the request runs synchronously server-side (typical BOM is 20-60 rows; with cache hits this completes in seconds). The frontend shows a spinner on the button; on response, the table re-renders. If we later see BOMs >200 rows we can move to a background job + polling.

---

## 3. Data model additions

### New table: `part_prices`

Caches one supplier's offer for one MPN in one currency. A row contains the full price-break ladder as JSON so we don't bloat the table with one row per qty break.

```sql
CREATE TABLE part_prices (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  mpn             TEXT NOT NULL,            -- normalized: trimmed + uppercased
  manufacturer    TEXT NOT NULL,            -- as reported by Nexar
  supplier        TEXT NOT NULL,            -- "DigiKey" | "Mouser" | "Farnell" | "RS" | "Newark" | ...
  sku             TEXT NOT NULL,            -- supplier's own order code
  currency        TEXT NOT NULL,            -- ISO 4217 ("GBP", "USD", ...)
  price_breaks    JSONB NOT NULL,           -- [{"qty":1,"price":2.34}, {"qty":10,"price":2.10}, ...]
  stock           INTEGER,                  -- null if unknown
  lead_time_days  INTEGER,                  -- null if unknown
  supplier_url    TEXT,                     -- click-through to supplier product page
  source          TEXT NOT NULL,            -- "nexar" | "csv" | "manual"
  fetched_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (mpn, supplier, currency)
);

CREATE INDEX part_prices_mpn_currency ON part_prices (mpn, currency);
```

**No organization_id**: pricing is not org-specific data. Two orgs querying the same MPN benefit from a shared cache. (The MPN→IPN *mapping* is org-scoped — that remains in the `mappings` table.)

### New table: `pricing_runs`

One row per "Price BOM" click. Lets us render "Priced at … · N/M lines" footers and audit API spend.

```sql
CREATE TABLE pricing_runs (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  document_id         UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at        TIMESTAMPTZ,
  rows_total          INTEGER NOT NULL,
  rows_priced         INTEGER NOT NULL DEFAULT 0,
  rows_unavailable    INTEGER NOT NULL DEFAULT 0,
  rows_skipped        INTEGER NOT NULL DEFAULT 0,  -- no MPN confirmed
  nexar_calls_made    INTEGER NOT NULL DEFAULT 0,
  cache_hits          INTEGER NOT NULL DEFAULT 0,
  currency            TEXT NOT NULL,
  error_message       TEXT
);

CREATE INDEX pricing_runs_document_id ON pricing_runs (document_id, started_at DESC);
```

### `BOMRow` (no schema change; computed at read time)

Pricing data is **not** stored on `BOMRow`. Instead, when a document is fetched, the handler joins `part_prices` rows by MPN (case-insensitive) and decorates each `BOMRow` with a transient `pricing` field:

```go
type BOMRow struct {
  // ... existing fields ...
  Pricing *RowPricing `json:"pricing,omitempty"`  // transient, joined at read
}

type RowPricing struct {
  Offers          []SupplierOffer `json:"offers"`
  BestUnitPrice   *Money          `json:"bestUnitPrice,omitempty"`  // at the BOM row's qty
  BestStockSupplier string        `json:"bestStockSupplier,omitempty"`
  FetchedAt       time.Time       `json:"fetchedAt"`
}

type SupplierOffer struct {
  Supplier      string       `json:"supplier"`
  SKU           string       `json:"sku"`
  PriceBreaks   []PriceBreak `json:"priceBreaks"`
  Stock         *int         `json:"stock,omitempty"`
  LeadTimeDays  *int         `json:"leadTimeDays,omitempty"`
  SupplierURL   string       `json:"supplierUrl"`
  Source        string       `json:"source"`  // "nexar" | "csv"
}

type PriceBreak struct {
  Quantity int     `json:"quantity"`
  Price    float64 `json:"price"`
}

type Money struct {
  Amount   float64 `json:"amount"`
  Currency string  `json:"currency"`
}
```

**Why transient, not persisted on BOMRow**: pricing drifts daily; BOMRows are persisted JSON in the `documents` table. If we baked pricing into the document we'd either (a) need to re-save the document after every cache refresh (write amplification + invalidation pain) or (b) end up showing stale data after re-runs. Joining at read time keeps `documents` a pure record of operator-confirmed BOM content and `part_prices` a pure pricing cache.

---

## 4. Nexar / Octopart integration

### Auth

Nexar uses OAuth 2.0 Client Credentials. Backend exchanges `NEXAR_CLIENT_ID` + `NEXAR_CLIENT_SECRET` for an access token at `https://identity.nexar.com/connect/token`. Tokens are valid 24h; cache in-memory with a 1h refresh buffer.

### GraphQL endpoint

```
POST https://api.nexar.com/graphql
Authorization: Bearer <token>
```

### Query shape

One call per row (per MPN). Currency comes from `OrgSettings.currency` (default `"GBP"`).

```graphql
query PriceByMpn($mpn: String!, $currency: String!) {
  supSearchMpn(q: $mpn, limit: 1) {
    results {
      part {
        mpn
        manufacturer { name }
        sellers(includeBrokers: false, authorizedOnly: true) {
          company { name }
          offers {
            sku
            inventoryLevel
            factoryLeadDays
            clickUrl
            prices {
              quantity
              convertedPrice(currency: $currency)
              convertedCurrency
            }
          }
        }
      }
    }
  }
}
```

**Why `authorizedOnly: true`**: drops brokers / grey-market resellers Andrew won't use. Keeps the supplier list short and trustworthy.

**Why one MPN per call** instead of batching: Nexar's batch endpoint (`supMultiMatch`) is more efficient at scale, but error handling per-row is messier (one bad MPN poisons the batch). For v1 stick with per-MPN calls; revisit if pricing latency becomes a problem.

### Response mapping

For each `sellers[].offers[]`:

- `supplier` = `company.name` (normalized: `"Digi-Key"` → `"DigiKey"`, etc.)
- `sku` = `offer.sku`
- `priceBreaks` = `offer.prices.map(p => ({ quantity: p.quantity, price: p.convertedPrice }))`
- `stock` = `offer.inventoryLevel` (null if 0 or missing)
- `leadTimeDays` = `offer.factoryLeadDays`
- `supplierUrl` = `offer.clickUrl`
- `source` = `"nexar"`

### Rate limits

Nexar's Production plan is metered per call, not RPS-throttled in practice. For safety, the client caps in-flight calls at 8 concurrent. A 50-row BOM with full cache miss → 50 calls → ~7s wall time. With 80% cache hit rate → ~10 calls → ~1.5s.

### Error handling

- **Token fetch fails**: pricing run fails; return 502 with `"pricing source unavailable"`; no rows are flagged (operator can retry).
- **Per-MPN call fails (network, 5xx)**: retry once with backoff; if still failing, fall through to CSV. Increment `nexar_calls_made` regardless.
- **Per-MPN call returns zero offers**: fall through to CSV; if CSV also empty → flag `pricing_unavailable`.
- **Per-MPN call returns offers but no supplier matches the org's accepted list** (future feature): treat as zero offers.

---

## 5. Caching contract

### Key

`(mpn_normalized, currency)` — i.e. one row in `part_prices` per `(MPN, supplier, currency)`. The `mpn` column is `UPPER(TRIM(mpn))`.

### TTL

24 hours, measured against `fetched_at`. Rationale: pricing changes happen on the order of weeks, not hours; 24h is plenty fresh for SAP entry and cuts API spend ~10–20× under typical usage.

### Lookup algorithm

```
priceRow(mpn, currency):
  rows = SELECT * FROM part_prices
         WHERE mpn = UPPER(TRIM($mpn))
           AND currency = $currency
           AND fetched_at > now() - INTERVAL '24 hours'
  if rows is non-empty:
    return rows, source="cache"
  fetch from Nexar
  if Nexar returns offers:
    UPSERT each offer ON (mpn, supplier, currency)
    return offers, source="nexar"
  fetch from CSV fallback
  if CSV returns offers:
    UPSERT each offer ON (mpn, supplier, currency) with source="csv"
    return offers, source="csv"
  return [], source="none"
```

**UPSERT semantics**: refetching a stale row overwrites the existing row (`ON CONFLICT (mpn, supplier, currency) DO UPDATE`). We never keep historical pricing snapshots — that's a separate feature.

### Invalidation

- **Time-based**: 24h `fetched_at` window (above).
- **Manual**: an admin endpoint `DELETE /api/admin/pricing-cache?mpn=...` to nuke a specific MPN when Andrew spots stale data.
- **Source switch**: when Nexar returns offers and the existing cached rows came from CSV, the CSV rows are replaced (UPSERT collapses the duplicate). CSV is the floor, Nexar is the ceiling.

### What is NOT cached

- Nexar tokens (in-memory, per process; 24h validity with 1h refresh buffer).
- Computed "best price at qty N" (cheap to derive from cached price-break ladder; caching the derived value would force re-computation on every qty change).

---

## 6. Fallback CSV format

Andrew already exports daily price lists from RS and Farnell. We ingest these via a one-shot upload UI on the Settings page, the same way client mappings are imported today.

### Expected columns

| Column | Required | Notes |
|--------|----------|-------|
| `mpn` | yes | Manufacturer part number, case-insensitive |
| `supplier` | yes | `"RS"` or `"Farnell"` (whitelist enforced) |
| `sku` | yes | Supplier order code |
| `currency` | no | Defaults to org currency |
| `qty_1` | yes | Unit price at qty 1 |
| `qty_10` | no | Unit price at qty 10 |
| `qty_100` | no | Unit price at qty 100 |
| `qty_1000` | no | Unit price at qty 1000 |
| `stock` | no | Integer |
| `lead_time_days` | no | Integer |
| `supplier_url` | no | Direct link to product page |

Import handler walks the CSV, builds `PriceBreak` arrays from the `qty_N` columns (dropping nulls), and UPSERTs into `part_prices` with `source="csv"`.

**Why this is a "fallback" not the primary**: it's a static snapshot. Operators must remember to re-upload. Nexar refreshes on its own. CSV's role is to fill the long tail of niche parts Octopart doesn't carry (custom cables, OEM connectors, regional distributors).

---

## 7. Flag handling

Add one new flag to the existing per-row `Flags` array:

| Flag | Meaning | Surfaced where |
|------|---------|----------------|
| `pricing_unavailable` | Pricing run completed but no offers found (Nexar empty AND CSV empty). MPN was non-empty. | Per-row pricing column shows `"No price available"` + supplier quick-links. WarningsPanel surfaces a count. |

Flag is set **only after** a pricing run that reached the "no offers" terminal state. We do not set it for rows that were skipped (no MPN confirmed) — those are reported separately on the pricing run summary as `rows_skipped`.

The flag is **cleared** on the next successful pricing run for that row (i.e. if Andrew uploads a CSV that now covers the MPN and re-prices the BOM).

---

## 8. HTTP API surface

### New endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/documents/{id}/price` | Run pricing for every row with a confirmed MPN. Body: optional `{"currency":"GBP"}` (defaults to org currency). Returns the updated `Document` with `pricing` joined onto each row, plus a `PricingRun` summary at the top level. Org-scoped. |
| `GET` | `/api/documents/{id}/pricing-runs` | List recent pricing runs for a document. Used by the workflow footer. Returns `PricingRun[]` ordered by `started_at DESC`, limit 20. |
| `POST` | `/api/pricing/csv-upload` | Bulk-import the RS/Farnell CSV fallback. Multipart `file` field. Returns `{"saved":N,"updated":N,"skipped":N,"errors":[...]}`. Admin-gated. |
| `DELETE` | `/api/admin/pricing-cache` | Query param `mpn` (required). Deletes all `part_prices` rows for that MPN across all currencies. Admin-gated. |

### Modified endpoint

| Method | Path | Change |
|--------|------|--------|
| `GET` | `/api/documents/{id}` | Now joins `part_prices` and the most-recent `pricing_runs` row. Response shape unchanged except for the new `BOMRow.pricing` and `Document.lastPricingRun` fields. |

### Document response addition

```json
{
  "id": "...",
  "bomRows": [
    {
      "id": "row-1",
      "manufacturerPartNumber": "CF130.07.05.UL",
      "quantity": { "raw": "120m", "value": 120, "unit": "m" },
      "pricing": {
        "offers": [
          {
            "supplier": "Farnell",
            "sku": "1234567",
            "priceBreaks": [{"quantity":1,"price":2.34},{"quantity":100,"price":1.95}],
            "stock": 4200,
            "leadTimeDays": 14,
            "supplierUrl": "https://...",
            "source": "nexar"
          }
        ],
        "bestUnitPrice": {"amount": 1.95, "currency": "GBP"},
        "bestStockSupplier": "Farnell",
        "fetchedAt": "2026-05-13T14:22:01Z"
      },
      "flags": []
    }
  ],
  "lastPricingRun": {
    "id": "...",
    "startedAt": "2026-05-13T14:22:00Z",
    "completedAt": "2026-05-13T14:22:09Z",
    "rowsTotal": 45,
    "rowsPriced": 42,
    "rowsUnavailable": 2,
    "rowsSkipped": 1,
    "nexarCallsMade": 8,
    "cacheHits": 37,
    "currency": "GBP"
  }
}
```

---

## 9. Frontend architecture

### New components

| Component | Responsibility |
|-----------|---------------|
| `PricingButton.tsx` | Toolbar button that triggers the pricing run. Shows a spinner during the call. Disabled when no rows have confirmed MPNs. |
| `PricingCell.tsx` | Cell renderer for the new "Best Price" column. Click to expand. |
| `PricingDetailsPanel.tsx` | Inline expansion under a BOM row showing the full supplier × qty grid. Highlights best price / best stock / shortest lead. |
| `NoPriceCell.tsx` | Renders `"No price available"` plus quick-links: RS search, Farnell search, Octopart search, Google. URL templates configurable in org settings. |
| `PricingFooter.tsx` | Lives at the bottom of the workflow page. Shows "Priced at …", per-row count, BOM total at qty 1 (and at the operator's chosen qty when set). |

### Modified components

- **`BomTable.tsx`** — adds a "Best Price" column (toggleable; off until first pricing run completes). The existing `<row>` markup gains an "expand pricing" affordance.
- **`WarningsPanel.tsx`** — surfaces `pricing_unavailable` count alongside existing flag counts.

### State model

No new global state. The pricing data lives inside `Document.bomRows[].pricing`, which is part of the existing document state. After `POST /api/documents/{id}/price` resolves, `setDocument(updated)` triggers re-render of the whole table.

### Quick-link URL templates

Org-level setting (extends `OrgSettings`):

```typescript
type SearchUrlTemplate = string  // contains "{mpn}" placeholder

interface OrgSettings {
  // ... existing fields ...
  pricingQuickLinks: {
    rsSearch:       SearchUrlTemplate  // default: "https://uk.rs-online.com/web/c/?searchTerm={mpn}"
    farnellSearch:  SearchUrlTemplate  // default: "https://uk.farnell.com/search?st={mpn}"
    octopartSearch: SearchUrlTemplate  // default: "https://octopart.com/search?q={mpn}"
    googleSearch:   SearchUrlTemplate  // default: "https://www.google.com/search?q={mpn}+datasheet"
  }
}
```

`{mpn}` is URL-encoded at substitution time.

---

## 10. Environment variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
_This is the **live** spec (supersedes the historical Nexar sections above)._

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `PRICING_PROVIDER` | no | `auto` | Empty/`auto`/`multi` composes every credentialed provider. A single name (`mouser`/`farnell`/`digikey`/`tme`) pins one source. `mock` = canned fixtures; `csv-only` = no upstream. Unknown/missing-cred → mock. |
| `MOUSER_API_KEY` | no¹ | — | Mouser Search API key. `MOUSER_SEARCH_URL` overrides the endpoint (testing). |
| `FARNELL_API_KEY` | no¹ | — | Farnell/element14 API key. `FARNELL_STORE_ID` (default `uk.farnell.com`) fixes the price currency; `FARNELL_STORE_CURRENCY`/`FARNELL_SEARCH_URL` for tests. |
| `DIGIKEY_CLIENT_ID` / `DIGIKEY_CLIENT_SECRET` | no¹ | — | Digi-Key OAuth2. `DIGIKEY_TOKEN_URL`/`DIGIKEY_SEARCH_URL` override endpoints (testing). |
| `TME_TOKEN` / `TME_APP_SECRET` | no¹ | — | TME token + HMAC signing secret. `TME_BASE_URL` overrides the base (testing). |
| `PRICING_CACHE_TTL_HOURS` | no | `24` | TTL for `part_prices` rows. |

¹ Each provider's credentials are individually optional. The composite uses
whichever are present; with none, the backend falls back to `mock`. Digi-Key
needs **both** client id and secret, and TME both token and app-secret, or
they're skipped.

The `PRICING_PROVIDER=mock` mode is the pricing-pipeline equivalent of `mockAnalysis()` — lets a developer run the full UX flow with zero credentials, returning canned `MOCK-MULTI`/`MOCK-SINGLE` offers.

### Multi-provider composition

With no `PRICING_PROVIDER` set, `selectPricingProvider` composes a `multiProvider` from every source whose credentials are present, in the fixed order **Mouser → Farnell → Digi-Key → TME**. The composite fans out concurrently, merges all offers, and dedupes on `(supplier, sku)` keeping the first occurrence — so the declaration order makes the dedupe deterministic regardless of which provider's goroutine finishes first. One credentialed provider is returned unwrapped (no `multi(x)` noise). Partial failure is tolerated: the run only errors (→ 502) if **every** child provider fails; one dead distributor API still returns the survivors.

---

## 11. Open questions

These are decisions worth making before code lands. None block the doc but each will shape the first PR.

1. **Currency**: org-level setting? Per-BOM override? For v1 I propose org-level only — defaults to GBP, configurable in the Settings page. Per-BOM override can wait.
2. **Supplier whitelist**: should Andrew be able to say "only show RS and Farnell prices, hide DigiKey"? Useful for filtering noise, but adds UI. Defer to v2.
3. **Best-price tie-breaking**: when two suppliers have identical prices, prefer (a) higher stock, (b) shorter lead time, (c) alphabetical. Confirm with Andrew.
4. **Stock thresholds**: should we flag rows where `stock < quantity` as `stock_insufficient`? Useful for buyers but adds a UX surface. Probably v2.
5. **Pricing for assemblies**: some BOM rows are sub-assemblies (no single MPN). Today they're just left blank. Pricing skips them (`rows_skipped`). Long-term these need their own pricing logic — out of scope for v1.
6. **Audit / cost tracking**: do we want to expose Nexar API call counts in the admin panel? `pricing_runs` already records `nexar_calls_made`; a small admin tile would let us watch spend without leaving the app.
7. **Re-pricing UX**: do we warn the operator when re-pricing a document that was last priced <24h ago (i.e. "everything will come from cache, are you sure")? Probably not — the cost is zero and the latency is sub-second.
8. **Failed-run state**: if the pricing run errors out mid-flight (Nexar 502, say), do we keep partial offers we already fetched, or roll back? I'd keep partials — the operator can re-run and pick up where we left off.

---

## 12. Phased rollout

### Phase 1 — minimum viable pricing (target: 1 PR)

- `part_prices` and `pricing_runs` tables + migrations.
- Nexar client + token cache + GraphQL query.
- Cache lookup with 24h TTL.
- `POST /api/documents/{id}/price` endpoint.
- `PRICING_PROVIDER=mock` mode for local dev.
- `pricing_unavailable` flag.
- `PricingButton` + a basic "Best Price" column in `BomTable`. No expansion panel yet; click the cell to open the supplier URL.
- Env vars + admin page section documenting Nexar setup.

### Phase 2 — full UX (target: 1 PR)

- `PricingDetailsPanel` with the full supplier × qty grid.
- `NoPriceCell` quick-links.
- `PricingFooter` with run summary + BOM total.
- `WarningsPanel` integration.
- Quick-link URL templates in `OrgSettings`.

### Phase 3 — fallback CSV + admin tools (target: 1 PR)

- `POST /api/pricing/csv-upload` + Settings-page UI.
- `DELETE /api/admin/pricing-cache` + admin-page UI.
- `pricing_runs` history view in the admin panel.

### Phase 4 — polish (target: 1 PR)

- Supplier whitelist (open Q #2).
- Per-BOM currency override (open Q #1).
- Stock-insufficient flag (open Q #4).
- API spend tile (open Q #6).

### Multi-provider (delivered)

Driven out of the Nexar free-tier 10-part cap. Direct-distributor
providers were added behind the existing `pricingProvider` interface —
**Mouser** (`mouser.go`, apiKey query param), **Farnell/element14**
(`farnell.go`, store-bound GBP), **Digi-Key** (`digikey.go`, OAuth2 +
`X-DIGIKEY-*` locale headers, one offer per packaging variation), and
**TME** (`tme.go`, HMAC-SHA1 signed two-step Search→Prices) — plus a
`multiProvider` (`multiprovider.go`) that fans out concurrently, merges,
and dedupes `(supplier, sku)` first-wins. `selectPricingProvider`
auto-composes from whatever credentials are present (direct distributors
before the Nexar aggregator). All five direct/composite paths are
TDD-covered with httptest fixtures plus skipped-without-creds live
integration tests. This makes Nexar optional rather than load-bearing:
the free distributor APIs cover the bulk of parts with no per-call cost
or shared quota.
