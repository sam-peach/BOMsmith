-- Nexar has been removed in favour of home-grown direct-distributor
-- providers (Mouser, Farnell, Digi-Key, TME) behind the same
-- pricingProvider interface. The pricing_runs counter that tracked
-- "cache misses that hit the upstream" was Nexar-specific in name only —
-- it has always meant "calls made to whatever provider is configured".
-- Rename it to match reality now that the misnomer would be actively
-- confusing.

ALTER TABLE pricing_runs
    RENAME COLUMN nexar_calls_made TO provider_calls_made;
