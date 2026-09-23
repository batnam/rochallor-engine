-- A completed parent commits its chain request before any child can start.
-- The child instance, its initial dispatch and this acknowledgement commit together.
CREATE TABLE workflow_chain (
    source_instance_id TEXT PRIMARY KEY REFERENCES workflow_instance(id),
    target_definition_id TEXT NOT NULL,
    variables JSONB NOT NULL,
    business_key TEXT,
    child_instance_id TEXT REFERENCES workflow_instance(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_workflow_chain_pending ON workflow_chain(created_at, source_instance_id)
    WHERE child_instance_id IS NULL;
