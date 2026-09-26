import { MonitorDatabase } from "../src/common/database/monitor-database";
import { OverviewQueries } from "../src/modules/overview/overview.queries";
import { ProcessInstanceQueries } from "../src/modules/process-instances/process-instance.queries";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";
import { verifyQueryBudget } from "./support/query-plan";

describe("Overview query budgets on 100,000 parallel instances", () => {
  let postgres: PostgresFixture;
  let database: MonitorDatabase;
  beforeAll(async () => {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_definition (id,version,name,raw_json,parsed_steps)
        SELECT 'flow-'||n,version,'Flow '||n,'{}','[]' FROM generate_series(0,19) n CROSS JOIN generate_series(1,2) version;
      INSERT INTO workflow_instance (id,definition_id,definition_version,status,current_step_ids,started_at)
        SELECT 'i-'||n,'flow-'||(n%20),((n/20)%2)+1,
          (ARRAY['ACTIVE','WAITING','FAILED','COMPLETED','CANCELLED'])[n%5+1],ARRAY['work','wait'],
          '2026-01-01'::timestamptz + n * interval '1 second' FROM generate_series(1,100000) n;
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at,attempt_number)
        SELECT 'work-'||n||'-'||attempt,'i-'||n,'work','SERVICE_TASK',
          CASE WHEN attempt=3 THEN 'RUNNING' ELSE 'FAILED' END,
          '2026-01-01'::timestamptz + n * interval '1 second',attempt
        FROM generate_series(1,100000) n CROSS JOIN generate_series(1,3) attempt;
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at)
        SELECT 'wait-'||n,'i-'||n,'wait','WAIT','RUNNING','2026-01-01' FROM generate_series(1,100000) n;
      ANALYZE workflow_instance; ANALYZE workflow_definition; ANALYZE step_execution;
    `);
    database = new MonitorDatabase(postgres.readOnlyDsn);
  }, 60000);
  afterAll(async () => {
    await database?.onApplicationShutdown();
    await postgres?.stop();
  });

  it("bounds workflow aggregates", async () => {
    await verifyQueryBudget(
      "overview-definitions",
      database,
      () => new OverviewQueries(database).definitions({}),
      { medianMs: 500, sharedBlocks: 10000, scannedRows: 150000 },
      false,
    );
  });
  it.each([
    ["id", "flow-1"],
    ["name", "Flow 1"],
  ])("bounds workflow search aggregates for %s", async (field, workflow) => {
    await verifyQueryBudget(
      `overview-search-${field}`,
      database,
      () => new OverviewQueries(database).definitions({ workflow }),
      { medianMs: 500, sharedBlocks: 10000, scannedRows: 150000 },
      false,
    );
  });
  it("bounds current-step aggregates with repeated attempts", async () => {
    await verifyQueryBudget(
      "overview-steps",
      database,
      () =>
        new OverviewQueries(database).steps({
          definitionId: "flow-1",
          definitionVersion: "1",
        }),
      { medianMs: 500, sharedBlocks: 50000, scannedRows: 100000 },
      false,
    );
  });
  it("bounds age drill-down using the actual instance query", async () => {
    await verifyQueryBudget(
      "instances-step-age",
      database,
      () =>
        new ProcessInstanceQueries(database).list({
          definitionId: "flow-1",
          definitionVersion: "1",
          currentStepId: "work",
          stepStartedBefore: "2026-09-01T00:00:00Z",
        }),
      { medianMs: 500, sharedBlocks: 50000, scannedRows: 100000 },
    );
  });
});
