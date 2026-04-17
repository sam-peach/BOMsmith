CREATE TABLE part_catalog (
    id                       UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id          UUID             NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    internal_part_number     TEXT             NOT NULL,
    manufacturer_part_number TEXT             NOT NULL DEFAULT '',
    description              TEXT             NOT NULL DEFAULT '',
    fingerprint              JSONB            NOT NULL DEFAULT '{}',
    usage_count              INTEGER          NOT NULL DEFAULT 0,
    last_used_at             TIMESTAMPTZ,
    created_at               TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ      NOT NULL DEFAULT now(),
    UNIQUE (organization_id, internal_part_number)
);

-- Fast lookup by manufacturer part number.
CREATE INDEX idx_part_catalog_mpn ON part_catalog (organization_id, UPPER(manufacturer_part_number));

-- Fast fingerprint type filter used by suggestFromCatalog.
CREATE INDEX idx_part_catalog_fp_type ON part_catalog (organization_id, (fingerprint->>'type'));
