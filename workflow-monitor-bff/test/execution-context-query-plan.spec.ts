import { MonitorDatabase } from "../src/common/database/monitor-database";
import { EXECUTION_CONTEXT_SQL } from "../src/modules/process-instances/execution-context";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";
import { verifyQueryBudget } from "./support/query-plan";

it("bounds parallel context amid 100,000 instances, retired deliveries, tasks and timers", async () => {
  let postgres: PostgresFixture | undefined;
  let database: MonitorDatabase | undefined;
  try {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_instance (id,definition_id,definition_version,status,current_step_ids)
        SELECT 'i-'||n,'flow',1,'ACTIVE',ARRAY['work','wait','human'] FROM generate_series(1,100000) n;
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status)
        SELECT 's-'||n,'i-'||n,'work','SERVICE_TASK','RUNNING' FROM generate_series(1,100000) n;
      INSERT INTO job (id,instance_id,step_execution_id,job_type,status)
        SELECT 'old-'||n,'i-'||n,'s-'||n,'work','FAILED' FROM generate_series(1,100000) n;
      INSERT INTO job (id,instance_id,step_execution_id,job_type,status)
        SELECT 'new-'||n,'i-'||n,'s-'||n,'work','UNLOCKED' FROM generate_series(1,100000) n;
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status)
        SELECT 'wait-'||n,'i-'||n,'wait','WAIT','RUNNING' FROM generate_series(1,100000) n;
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status)
        SELECT 'human-'||n,'i-'||n,'human','USER_TASK','RUNNING' FROM generate_series(1,100000) n;
      INSERT INTO user_task (id,instance_id,step_execution_id,step_id)
        SELECT 'task-'||n,'i-'||n,'human-'||n,'human' FROM generate_series(1,100000) n;
      INSERT INTO boundary_event_schedule (id,instance_id,step_execution_id,target_step_id,fire_at)
        SELECT 'timer-'||n,'i-'||n,'wait-'||n,'work','2099-01-01' FROM generate_series(1,100000) n;
      ANALYZE workflow_instance; ANALYZE step_execution; ANALYZE job;
      ANALYZE user_task; ANALYZE boundary_event_schedule;
    `);
    database = new MonitorDatabase(postgres.readOnlyDsn);
    const db = database;
    await verifyQueryBudget(
      "execution-context",
      db,
      () =>
        db.query(EXECUTION_CONTEXT_SQL, ["i-50000", ["work", "wait", "human"]]),
      { medianMs: 250, sharedBlocks: 10000, scannedRows: 250000 },
      false,
    );
  } finally {
    await database?.onApplicationShutdown();
    await postgres?.stop();
  }
}, 60000);
