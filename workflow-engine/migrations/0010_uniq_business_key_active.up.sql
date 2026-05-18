-- 0010: enforce business_key uniqueness per definition for in-flight instances.
--
-- Two instances of the same definition cannot share a business_key while the
-- first is still in-flight. "In-flight" means non-terminal: ACTIVE (currently
-- executing) or WAITING (parked on a USER_TASK / WAIT step). Historical
-- re-runs after the prior instance reaches COMPLETED / FAILED / CANCELLED
-- remain allowed. NULL business_keys are excluded so chained definitions
-- without a correlation key are unaffected.

CREATE UNIQUE INDEX IF NOT EXISTS uniq_workflow_instance_bk_def_active
    ON workflow_instance (business_key, definition_id)
    WHERE business_key IS NOT NULL AND status IN ('ACTIVE', 'WAITING');
