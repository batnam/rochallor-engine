-- 0001: workflow_definition
-- Stores immutable, versioned workflow definitions.

CREATE TABLE IF NOT EXISTS workflow_definition (
    pk                           BIGSERIAL        PRIMARY KEY,
    id                           TEXT             NOT NULL,
    version                      INT              NOT NULL,
    name                         TEXT             NOT NULL,
    description                  TEXT,
    raw_json                     JSONB            NOT NULL,
    parsed_steps                 JSONB            NOT NULL,
    auto_start_next_workflow_id  TEXT,
    uploaded_at                  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    uploaded_by                  TEXT,

    CONSTRAINT uq_workflow_definition_id_version UNIQUE (id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflow_definition_id
    ON workflow_definition (id);

CREATE INDEX IF NOT EXISTS idx_workflow_definition_uploaded_at
    ON workflow_definition (uploaded_at DESC);
