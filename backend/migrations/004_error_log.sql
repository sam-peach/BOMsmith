CREATE TABLE error_log (
    id        BIGSERIAL   PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
    level     TEXT        NOT NULL,
    component TEXT        NOT NULL,
    message   TEXT        NOT NULL,
    doc_name  TEXT        NOT NULL DEFAULT ''
);
