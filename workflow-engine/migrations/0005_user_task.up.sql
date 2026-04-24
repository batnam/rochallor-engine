-- 0005: user_task
-- Tracks human tasks created at USER_TASK steps.

CREATE TABLE IF NOT EXISTS user_task (
    id                  TEXT        PRIMARY KEY,           -- ULID
    instance_id         TEXT        NOT NULL REFERENCES workflow_instance(id),
    step_execution_id   TEXT        NOT NULL REFERENCES step_execution(id),
    step_id             TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'OPEN', -- OPEN | COMPLETED | CANCELLED
    assignee            TEXT,
    assignee_group      TEXT,
    payload             JSONB,
    result              JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_user_task_instance
    ON user_task (instance_id);

CREATE INDEX IF NOT EXISTS idx_user_task_status
    ON user_task (status)
    WHERE status = 'OPEN';
