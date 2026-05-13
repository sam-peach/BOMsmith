-- Per-client mapping buckets and per-drawing client tagging.
--
-- Two clients (customers of the org) can use the same CPN for different parts.
-- Adding client_label to mappings lets each client's part numbers live in their
-- own bucket; empty string is the generic / pooled bucket and remains the
-- fallback for untagged drawings. The unique key shifts from
-- (org, cpn) to (org, client_label, cpn) so the same CPN can coexist
-- across clients without collision.

ALTER TABLE mappings
    ADD COLUMN client_label TEXT NOT NULL DEFAULT '';

ALTER TABLE mappings
    DROP CONSTRAINT mappings_organization_id_customer_part_number_key;

ALTER TABLE mappings
    ADD CONSTRAINT mappings_org_client_cpn_key
        UNIQUE (organization_id, client_label, customer_part_number);

CREATE INDEX mappings_org_client_idx
    ON mappings(organization_id, client_label);

ALTER TABLE documents
    ADD COLUMN client_label TEXT NOT NULL DEFAULT '';
