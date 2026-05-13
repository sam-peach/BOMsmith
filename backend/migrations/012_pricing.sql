-- BOM pricing (Phase 1 of the design in docs/pricing.md).
--
-- part_prices is the cache layer in front of Nexar/Octopart. Each row holds
-- one supplier's offer for one MPN in one currency, including the full
-- price-break ladder as JSON. (mpn, supplier, currency) is unique — fetching
-- a stale row UPSERTs in place. Pricing is intentionally NOT org-scoped:
-- two orgs querying the same MPN benefit from a shared cache.
--
-- pricing_runs records one row per "Price BOM" click for audit, the
-- workflow-page footer, and API-spend tracking.

CREATE TABLE part_prices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mpn             TEXT NOT NULL,
    manufacturer    TEXT NOT NULL DEFAULT '',
    supplier        TEXT NOT NULL,
    sku             TEXT NOT NULL,
    currency        TEXT NOT NULL,
    price_breaks    JSONB NOT NULL,
    stock           INTEGER,
    lead_time_days  INTEGER,
    supplier_url    TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL,
    fetched_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mpn, supplier, currency)
);

CREATE INDEX part_prices_mpn_currency_idx ON part_prices (mpn, currency);

CREATE TABLE pricing_runs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id         TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    organization_id     TEXT NOT NULL,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ,
    rows_total          INTEGER NOT NULL DEFAULT 0,
    rows_priced         INTEGER NOT NULL DEFAULT 0,
    rows_unavailable    INTEGER NOT NULL DEFAULT 0,
    rows_skipped        INTEGER NOT NULL DEFAULT 0,
    nexar_calls_made    INTEGER NOT NULL DEFAULT 0,
    cache_hits          INTEGER NOT NULL DEFAULT 0,
    currency            TEXT NOT NULL,
    error_message       TEXT
);

CREATE INDEX pricing_runs_document_idx ON pricing_runs (document_id, started_at DESC);
