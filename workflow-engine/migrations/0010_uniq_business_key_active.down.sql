-- 0010: drop the (business_key, definition_id) WHERE status='ACTIVE' uniqueness.

DROP INDEX IF EXISTS uniq_workflow_instance_bk_def_active;
