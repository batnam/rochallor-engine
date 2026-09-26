import { MonitorDatabase } from "../src/common/database/monitor-database";
import type { ProcessInstanceQuery } from "../src/modules/process-instances/dto/process-instance.dto";
import { ProcessInstanceQueries } from "../src/modules/process-instances/process-instance.queries";
import { verifyQueryBudget } from "./support/query-plan";

import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Process Instance query-plan verification", () => {
  let postgres: PostgresFixture | undefined;
  let database: MonitorDatabase;
  let queries: ProcessInstanceQueries;

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
    database = new MonitorDatabase(postgres.readOnlyDsn);
    queries = new ProcessInstanceQueries(database);
  }, 30_000);

  afterAll(async () => {
    await database?.onApplicationShutdown();
    await postgres?.stop();
  });

  it.each<[string, ProcessInstanceQuery]>([
    ["unfiltered-first-page", {}],
    [
      "filtered-cursor-page",
      {
        definitionId: "definition-1",
        status: ["ACTIVE", "WAITING"],
        from: "2026-01-01T00:00:00Z",
        to: "2026-01-03T00:00:00Z",
      },
    ],
    ["exact-business-key", { businessKey: "business-50000" }],
  ])("keeps %s within its query budget", async (name, filters) => {
    const query = { ...filters };
    if (name === "filtered-cursor-page") {
      const first = await queries.list(query);
      expect(first.nextCursor).not.toBeNull();
      query.cursor = first.nextCursor ?? undefined;
    }
    await verifyQueryBudget(
      `instances-${name}`,
      database,
      () => queries.list(query),
      {
        medianMs: 250,
        sharedBlocks: name === "filtered-cursor-page" ? 2_000 : 250,
        scannedRows: name === "filtered-cursor-page" ? 10_000 : 1_000,
      },
    );
  });
});
