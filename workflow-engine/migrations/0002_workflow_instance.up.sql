-- 0002: workflow_instance
-- Tracks the live runtime state of every workflow execution.

CREATE TABLE IF NOT EXISTS workflow_instance (
    id                  TEXT        PRIMARY KEY,           -- ULID
    definition_id       TEXT        NOT NULL,
    definition_version  INT         NOT NULL,
    status              TEXT        NOT NULL,              -- ACTIVE | WAITING | COMPLETED | FAILED | CANCELLED
    current_step_ids    TEXT[]      NOT NULL DEFAULT '{}',
    variables           JSONB       NOT NULL DEFAULT '{}',
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ,
    failure_reason      TEXT,
    business_key        TEXT
);

CREATE INDEX IF NOT EXISTS idx_workflow_instance_def_status
    ON workflow_instance (definition_id, status);

CREATE INDEX IF NOT EXISTS idx_workflow_instance_business_key
    ON workflow_instance (business_key);

CREATE INDEX IF NOT EXISTS idx_workflow_instance_started_at
    ON workflow_instance (started_at DESC);
