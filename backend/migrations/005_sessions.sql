CREATE TABLE sessions (
    token      TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    org_id     TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
