-- 0003: step_execution
-- One row per entry into a step. Retries and parallel branches each create a new row.

CREATE TABLE IF NOT EXISTS step_execution (
    id               TEXT        PRIMARY KEY,           -- ULID
    instance_id      TEXT        NOT NULL REFERENCES workflow_instance(id),
    step_id          TEXT        NOT NULL,
    step_type        TEXT        NOT NULL,
    attempt_number   INT         NOT NULL DEFAULT 1,
    status           TEXT        NOT NULL,              -- RUNNING | COMPLETED | FAILED | SKIPPED
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    input_snapshot   JSONB,
    output_snapshot  JSONB,
    failure_reason   TEXT
);

CREATE INDEX IF NOT EXISTS idx_step_execution_instance_started
    ON step_execution (instance_id, started_at);

CREATE INDEX IF NOT EXISTS idx_step_execution_instance_step_attempt
    ON step_execution (instance_id, step_id, attempt_number);
