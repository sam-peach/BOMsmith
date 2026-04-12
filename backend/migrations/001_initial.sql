CREATE TABLE organizations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mappings (
    id                       UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id          UUID             NOT NULL REFERENCES organizations(id),
    customer_part_number     TEXT             NOT NULL,
    internal_part_number     TEXT             NOT NULL DEFAULT '',
    manufacturer_part_number TEXT             NOT NULL DEFAULT '',
    description              TEXT             NOT NULL DEFAULT '',
    source                   TEXT             NOT NULL DEFAULT 'manual',
    confidence               DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    last_used_at             TIMESTAMPTZ      NOT NULL DEFAULT now(),
    created_at               TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ      NOT NULL DEFAULT now(),
    UNIQUE (organization_id, customer_part_number)
);
