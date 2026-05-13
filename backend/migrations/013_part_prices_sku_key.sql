-- Widen the part_prices uniqueness boundary to include sku.
--
-- The live Nexar test surfaced suppliers (TME, Arrow, Verical, …) returning
-- two or more offers for the same MPN+currency under one company name —
-- typically different regional warehouses or SKUs at distinct price ladders.
-- With the original (mpn, supplier, currency) constraint, the second UPSERT
-- silently overwrites the first, losing offer data the user needs to compare.
--
-- Widening to (mpn, supplier, sku, currency) keeps each distinct offer as
-- its own row. The pricing-details panel shows them as separate lines under
-- the supplier name. Compact-cell "best price" still picks the cheapest
-- across all offers for the MPN, so the headline behaviour is unchanged.

ALTER TABLE part_prices
    DROP CONSTRAINT IF EXISTS part_prices_mpn_supplier_currency_key;

ALTER TABLE part_prices
    ADD CONSTRAINT part_prices_mpn_supplier_sku_currency_key
        UNIQUE (mpn, supplier, sku, currency);
