-- 0008: drop performance indexes.

DROP INDEX IF EXISTS idx_job_status_lock_expires_at;
DROP INDEX IF EXISTS idx_job_status_locked_at;
