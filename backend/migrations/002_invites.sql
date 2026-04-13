CREATE TABLE invite_tokens (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations(id),
    created_by      UUID        NOT NULL REFERENCES users(id),
    token           TEXT        NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    used_by         UUID        REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
