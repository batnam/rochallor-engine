-- 0009: dispatch_outbox — transactional outbox for event-driven dispatch (FR-002, FR-017).
-- Written inside the same tx that inserts a `job` row when DispatchMode=kafka_outbox.
-- Deleted by the relay inside the tx that commits the successful Kafka publish
-- (delete-on-publish; durable trail lives in audit_log).
-- In polling mode the table exists but is never touched (INV-4).

CREATE TABLE IF NOT EXISTS dispatch_outbox (
    id          TEXT        PRIMARY KEY,             -- ULID, also the Kafka dedup_id
    job_id      TEXT        NOT NULL REFERENCES job(id),
    instance_id TEXT        NOT NULL,                -- Kafka partition key (R-002)
    job_type    TEXT        NOT NULL,                -- routes to workflow.jobs.<jobType>
    payload     BYTEA       NOT NULL,                -- pre-serialized JobDispatchEvent proto
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Drives the relay's FIFO drain query:
-- SELECT … FROM dispatch_outbox ORDER BY created_at LIMIT $batch FOR UPDATE SKIP LOCKED
CREATE INDEX IF NOT EXISTS idx_dispatch_outbox_created_at
    ON dispatch_outbox (created_at);
