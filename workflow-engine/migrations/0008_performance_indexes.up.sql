-- 0008: performance indexes for sweeper and admin queries (FR-005).
-- The existing `(status, job_type) WHERE status = 'UNLOCKED'` and
-- `(lock_expires_at) WHERE status = 'LOCKED'` partial indexes already
-- cover the hot poll/sweep paths. These composite indexes support
-- broader status-scoped scans (e.g., debugging, observability queries,
-- and future sweeper variants gated on advisory locks per FR-004).

CREATE INDEX IF NOT EXISTS idx_job_status_locked_at
    ON job (status, locked_at);

CREATE INDEX IF NOT EXISTS idx_job_status_lock_expires_at
    ON job (status, lock_expires_at);
