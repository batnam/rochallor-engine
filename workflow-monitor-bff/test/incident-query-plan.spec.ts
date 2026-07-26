import { Pool } from "pg";

import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

interface ExplainResult {
  "QUERY PLAN": Array<{
    "Execution Time": number;
    Plan: {
      "Actual Rows": number;
      "Node Type": string;
    };
  }>;
}

const JOB_CONTEXT = `
  SELECT DISTINCT ON (step_execution_id)
    id,
    step_execution_id,
    job_type,
    status
  FROM job
  ORDER BY step_execution_id, created_at DESC, id DESC
`;

describe("Incident query-plan verification", () => {
  let postgres: PostgresFixture | undefined;
  let monitorPool: Pool | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_definition (
        id,
        version,
        name,
        raw_json,
        parsed_steps
      )
      SELECT
        'definition-' || series,
        1,
        'Definition ' || series,
        '{}',
        '[]'
      FROM generate_series(0, 19) AS series;

      INSERT INTO workflow_instance (
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        variables,
        started_at,
        completed_at
      )
      SELECT
        'incident-instance-' || lpad(series::text, 6, '0'),
        'definition-' || series % 20,
        1,
        CASE WHEN series % 10 = 0 THEN 'CANCELLED' ELSE 'FAILED' END,
        '{}',
        '{}',
        '2026-01-01T00:00:00Z'::timestamptz + series * interval '1 second',
        '2026-01-01T00:00:01Z'::timestamptz + series * interval '1 second'
      FROM generate_series(1, 100000) AS series;

      INSERT INTO step_execution (
        id,
        instance_id,
        step_id,
        step_type,
        attempt_number,
        status,
        started_at,
        ended_at,
        failure_reason
      )
      SELECT
        'incident-execution-' || lpad(series::text, 6, '0'),
        'incident-instance-' || lpad(series::text, 6, '0'),
        'step-' || series % 50,
        CASE WHEN series % 2 = 0 THEN 'SERVICE_TASK' ELSE 'SCRIPT_TASK' END,
        1,
        'FAILED',
        '2026-01-01T00:00:00Z'::timestamptz + series * interval '1 second',
        '2026-01-01T00:00:01Z'::timestamptz + series * interval '1 second',
        'representative failure'
      FROM generate_series(1, 100000) AS series;

      INSERT INTO job (
        id,
        instance_id,
        step_execution_id,
        job_type,
        status
      )
      SELECT
        'incident-job-' || lpad(series::text, 6, '0'),
        'incident-instance-' || lpad(series::text, 6, '0'),
        'incident-execution-' || lpad(series::text, 6, '0'),
        'job-type-' || series % 10,
        'FAILED'
      FROM generate_series(2, 100000, 2) AS series;

      ANALYZE workflow_instance;
      ANALYZE step_execution;
      ANALYZE job;
    `);
    monitorPool = new Pool({ connectionString: postgres.readOnlyDsn });
  }, 60_000);

  afterAll(async () => {
    await monitorPool?.end();
    await postgres?.stop();
  });

  it.each([
    [
      "unfiltered first page",
      `
        SELECT execution.id, related_job.job_type
        FROM step_execution AS execution
        JOIN workflow_instance AS instance
          ON instance.id = execution.instance_id
        JOIN workflow_definition AS definition
          ON definition.id = instance.definition_id
         AND definition.version = instance.definition_version
        LEFT JOIN (${JOB_CONTEXT}) AS related_job
          ON related_job.step_execution_id = execution.id
        WHERE execution.status = 'FAILED'
          AND instance.status <> 'CANCELLED'
        ORDER BY execution.ended_at DESC, execution.id DESC
        LIMIT 51
      `,
    ],
    [
      "filtered cursor page",
      `
        SELECT execution.id, related_job.job_type
        FROM step_execution AS execution
        JOIN workflow_instance AS instance
          ON instance.id = execution.instance_id
        JOIN workflow_definition AS definition
          ON definition.id = instance.definition_id
         AND definition.version = instance.definition_version
        LEFT JOIN (${JOB_CONTEXT}) AS related_job
          ON related_job.step_execution_id = execution.id
        WHERE execution.status = 'FAILED'
          AND instance.status <> 'CANCELLED'
          AND instance.definition_id = 'definition-2'
          AND related_job.job_type = 'job-type-2'
          AND execution.ended_at >= '2026-01-01T00:00:00Z'
          AND execution.ended_at < '2026-01-03T00:00:00Z'
          AND (execution.ended_at, execution.id) < (
            '2026-01-02T00:00:01Z',
            'incident-execution-086400'
          )
        ORDER BY execution.ended_at DESC, execution.id DESC
        LIMIT 51
      `,
    ],
  ])("captures an executable plan for the %s query", async (_name, sql) => {
    if (!monitorPool) {
      throw new Error("Monitor database pool did not start");
    }

    const result = await monitorPool.query<ExplainResult>(
      `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) ${sql}`,
    );
    const explanation = result.rows[0]["QUERY PLAN"][0];

    expect(explanation.Plan["Node Type"]).toBe("Limit");
    expect(explanation.Plan["Actual Rows"]).toBeLessThanOrEqual(51);
    expect(explanation["Execution Time"]).toBeGreaterThanOrEqual(0);
  });
});
