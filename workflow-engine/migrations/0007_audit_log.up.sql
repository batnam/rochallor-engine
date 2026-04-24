-- 0007: audit_log
-- Append-only record of notable Engine actions. Enabled by default; disable at deployment
-- time by setting WE_AUDIT_LOG_ENABLED=false (the Engine will skip inserts).

CREATE TABLE IF NOT EXISTS audit_log (
    id           BIGSERIAL   PRIMARY KEY,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor        TEXT        NOT NULL,              -- principal name or 'engine'
    kind         TEXT        NOT NULL,              -- e.g. 'definition-uploaded', 'instance-started'
    instance_id  TEXT,                              -- nullable for non-instance events
    detail       JSONB                              -- opaque, event-specific fields
);

CREATE INDEX IF NOT EXISTS idx_audit_log_instance
    ON audit_log (instance_id)
    WHERE instance_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_log_kind_at
    ON audit_log (kind, at DESC);
