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

describe("Process Instance query-plan verification", () => {
  let postgres: PostgresFixture | undefined;
  let monitorPool: Pool | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_instance (
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        variables,
        started_at,
        business_key
      )
      SELECT
        'plan-' || lpad(series::text, 6, '0'),
        'definition-' || series % 20,
        1,
        (ARRAY['ACTIVE', 'WAITING', 'COMPLETED', 'FAILED', 'CANCELLED'])[series % 5 + 1],
        '{}',
        '{}',
        '2026-01-01T00:00:00Z'::timestamptz + series * interval '1 second',
        'business-' || series
      FROM generate_series(1, 100000) AS series;
      ANALYZE workflow_instance;
    `);
    monitorPool = new Pool({ connectionString: postgres.readOnlyDsn });
  }, 30_000);

  afterAll(async () => {
    await monitorPool?.end();
    await postgres?.stop();
  });

  it.each([
    [
      "unfiltered first page",
      `
        SELECT id
        FROM workflow_instance
        ORDER BY started_at DESC, id DESC
        LIMIT 51
      `,
    ],
    [
      "filtered cursor page",
      `
        SELECT id
        FROM workflow_instance
        WHERE definition_id = 'definition-1'
          AND status = ANY(ARRAY['ACTIVE', 'WAITING'])
          AND started_at >= '2026-01-01T00:00:00Z'
          AND started_at < '2026-01-03T00:00:00Z'
          AND (started_at, id) < (
            '2026-01-02T00:00:00Z',
            'plan-086400'
          )
        ORDER BY started_at DESC, id DESC
        LIMIT 51
      `,
    ],
    [
      "exact business-key page",
      `
        SELECT id
        FROM workflow_instance
        WHERE business_key = 'business-50000'
        ORDER BY started_at DESC, id DESC
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
