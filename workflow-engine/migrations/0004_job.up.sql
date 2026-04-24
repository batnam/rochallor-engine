-- 0004: job
-- One job per SERVICE_TASK step execution. Workers poll, lock, and complete/fail jobs.
-- Poll query: SELECT … WHERE status = 'UNLOCKED' AND job_type = ANY($1) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $2

CREATE TABLE IF NOT EXISTS job (
    id                  TEXT        PRIMARY KEY,           -- ULID
    instance_id         TEXT        NOT NULL,
    step_execution_id   TEXT        NOT NULL REFERENCES step_execution(id),
    job_type            TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'UNLOCKED', -- UNLOCKED | LOCKED | COMPLETED | FAILED
    worker_id           TEXT,
    locked_at           TIMESTAMPTZ,
    lock_expires_at     TIMESTAMPTZ,
    retries_remaining   INT         NOT NULL DEFAULT 0,
    payload             JSONB       NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Drives SELECT … FOR UPDATE SKIP LOCKED poll query.
CREATE INDEX IF NOT EXISTS idx_job_unlocked_by_type
    ON job (status, job_type)
    WHERE status = 'UNLOCKED';

-- Drives the lease-expiry sweeper.
CREATE INDEX IF NOT EXISTS idx_job_lock_expires
    ON job (lock_expires_at)
    WHERE status = 'LOCKED';
