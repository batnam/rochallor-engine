import { MonitorDatabase } from "../src/common/database/monitor-database";
import type { IncidentQuery } from "../src/modules/incidents/dto/incident.dto";
import { IncidentQueries } from "../src/modules/incidents/incident.queries";
import { verifyQueryBudget } from "./support/query-plan";

import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Incident query-plan verification", () => {
  let postgres: PostgresFixture | undefined;
  let database: MonitorDatabase;
  let queries: IncidentQueries;

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
    database = new MonitorDatabase(postgres.readOnlyDsn);
    queries = new IncidentQueries(database);
  }, 60_000);

  afterAll(async () => {
    await database?.onApplicationShutdown();
    await postgres?.stop();
  });

  it.each<[string, IncidentQuery]>([
    ["unfiltered-first-page", {}],
    [
      "filtered-cursor-page",
      {
        definitionId: "definition-2",
        jobType: "job-type-2",
        from: "2026-01-01T00:00:00Z",
        to: "2026-01-03T00:00:00Z",
      },
    ],
  ])("keeps %s within its query budget", async (name, filters) => {
    const query = { ...filters };
    if (name === "filtered-cursor-page") {
      const first = await queries.list(query);
      expect(first.nextCursor).not.toBeNull();
      query.cursor = first.nextCursor ?? undefined;
    }
    await verifyQueryBudget(
      `incidents-${name}`,
      database,
      () => queries.list(query),
      {
        medianMs: name === "filtered-cursor-page" ? 500 : 1_000,
        sharedBlocks: name === "filtered-cursor-page" ? 50_000 : 500_000,
        scannedRows: name === "filtered-cursor-page" ? 100_000 : 400_000,
      },
    );
  });
});
