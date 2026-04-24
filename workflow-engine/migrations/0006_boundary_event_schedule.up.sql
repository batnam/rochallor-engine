-- 0006: boundary_event_schedule
-- Tracks pending TIMER boundary events. A sweeper loop fires them when fire_at <= now().

CREATE TABLE IF NOT EXISTS boundary_event_schedule (
    id                  TEXT        PRIMARY KEY,           -- ULID
    instance_id         TEXT        NOT NULL REFERENCES workflow_instance(id),
    step_execution_id   TEXT        NOT NULL REFERENCES step_execution(id),
    target_step_id      TEXT        NOT NULL,
    fire_at             TIMESTAMPTZ NOT NULL,
    interrupting        BOOLEAN     NOT NULL DEFAULT false,
    fired               BOOLEAN     NOT NULL DEFAULT false
);

-- Drives the boundary-event sweeper loop.
CREATE INDEX IF NOT EXISTS idx_boundary_event_fire_at
    ON boundary_event_schedule (fire_at)
    WHERE fired = false;
