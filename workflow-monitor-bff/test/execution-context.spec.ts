import type { INestApplication } from "@nestjs/common";
import request from "supertest";
import { createMonitorApp } from "../src/app";
import type { ExecutionContext } from "../src/modules/process-instances/execution-context";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Current execution evidence through the read-only API", () => {
  let postgres: PostgresFixture;
  let app: INestApplication;
  beforeAll(async () => {
    postgres = await startPostgresFixture();
    app = await createMonitorApp({
      postgresDsn: postgres.readOnlyDsn,
      log: () => undefined,
    });
    await app.init();
  });
  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });
  beforeEach(async () => {
    await postgres.query(`
      TRUNCATE workflow_instance CASCADE;
      DELETE FROM job;
      DELETE FROM workflow_definition;
      INSERT INTO workflow_definition (id,version,name,raw_json,parsed_steps) VALUES ('flow',1,'Flow','{"steps":[]}','[]');
      INSERT INTO workflow_instance (id,definition_id,definition_version,status,current_step_ids)
        VALUES ('live','flow',1,'WAITING',ARRAY['wait','human','service','join','missing']);
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at,ended_at) VALUES
        ('w','live','wait','WAIT','RUNNING','2026-01-01',NULL),
        ('h','live','human','USER_TASK','RUNNING','2026-01-01',NULL),
        ('s','live','service','SERVICE_TASK','RUNNING','2026-01-01',NULL),
        ('j','live','join','JOIN_GATEWAY','COMPLETED','2026-01-01','2026-01-01');
      INSERT INTO user_task (id,instance_id,step_execution_id,step_id,status,assignee_group)
        VALUES ('task','live','h','human','OPEN','reviewers');
      INSERT INTO job (id,instance_id,step_execution_id,job_type,status,worker_id,locked_at,lock_expires_at,retries_remaining) VALUES
        ('retired','live','s','payments','FAILED','old-worker','2026-01-01','2026-01-02',3),
        ('current','live','s','payments','LOCKED','worker-2','2026-01-02','2026-01-03',2);
      INSERT INTO boundary_event_schedule (id,instance_id,step_execution_id,target_step_id,fire_at,interrupting,fired) VALUES
        ('pending','live','h','escalate','2026-01-03',true,false),
        ('fired','live','h','escalate','2026-01-02',false,true),
        ('obsolete','live','j','escalate','2026-01-03',false,false);
    `);
  });

  async function detail() {
    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances/live")
      .expect(200);
    return response.body as {
      observedAt: string;
      executionContext: ExecutionContext[];
      instance: { status: string };
    };
  }

  it("explains parallel signal, human and worker steps, with only pending live timers", async () => {
    const response = await detail();
    expect(Date.parse(response.observedAt)).not.toBeNaN();
    expect(response.executionContext.map((item) => item.reason)).toEqual([
      "signal",
      "userTask",
      "jobLocked",
      "unavailable",
      "unavailable",
    ]);
    expect(response.executionContext[1]).toMatchObject({
      task: { assignee: null, assigneeGroup: "reviewers" },
      timers: [{ id: "pending", interrupting: true }],
    });
    expect(response.executionContext[2]).toMatchObject({
      job: { id: "current", workerId: "worker-2", retriesRemaining: 2 },
    });
    expect(response.executionContext[3]).toMatchObject({
      status: "COMPLETED",
      timers: [],
    });
    expect(response.executionContext[4]).toMatchObject({
      executionId: null,
      reason: "unavailable",
    });
  });

  it("explains an available service job while the instance remains ACTIVE", async () => {
    await postgres.query(
      "UPDATE workflow_instance SET status='ACTIVE', current_step_ids=ARRAY['service']; UPDATE job SET status='UNLOCKED',worker_id=NULL,locked_at=NULL,lock_expires_at=NULL WHERE id='current';",
    );
    const response = await detail();
    expect(response.instance.status).toBe("ACTIVE");
    expect(response.executionContext[0]).toMatchObject({
      reason: "jobAvailable",
      job: { workerId: null, lockExpiresAt: null },
    });
  });

  it.each(["COMPLETED", "FAILED", "CANCELLED"])(
    "hides active evidence for %s even when old rows remain live",
    async (status) => {
      await postgres.query(`UPDATE workflow_instance SET status='${status}'`);
      expect((await detail()).executionContext).toEqual([]);
    },
  );

  it("reports conflicting jobs/tasks without choosing one as the current owner", async () => {
    await postgres.query(`
      INSERT INTO job (id,instance_id,step_execution_id,job_type,status) VALUES ('conflict','live','s','payments','UNLOCKED');
      INSERT INTO user_task (id,instance_id,step_execution_id,step_id) VALUES ('conflict','live','h','human');
    `);
    const items = (await detail()).executionContext;
    for (const index of [1, 2])
      expect(items[index]).toMatchObject({
        reason: "unavailable",
        job: null,
        task: null,
        unavailableReason: "Conflicting live execution records",
      });
  });

  it("reports missing task/job evidence and caps pending timer payloads", async () => {
    await postgres.query(`DELETE FROM job; DELETE FROM user_task;
      INSERT INTO boundary_event_schedule (id,instance_id,step_execution_id,target_step_id,fire_at)
        SELECT 'timer-'||n,'live','w','end','2027-01-01' FROM generate_series(1,105) n;
    `);
    const items = (await detail()).executionContext;
    expect(items[0].timers).toHaveLength(100);
    expect(items[0].timersTruncated).toBe(true);
    expect(items[1].reason).toBe("unavailable");
    expect(items[2].reason).toBe("unavailable");
  });

  it("keeps failed attempts historical while a later attempt progresses and succeeds", async () => {
    await postgres.query(`
      UPDATE step_execution SET status='FAILED', ended_at=now(),failure_reason='old failure' WHERE id='s';
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status,attempt_number) VALUES ('s2','live','service','SERVICE_TASK','RUNNING',2);
    `);
    const first = await request(app.getHttpServer())
      .get("/api/v1/incidents/s")
      .expect(200);
    expect(first.body.incident).toMatchObject({
      historical: true,
      latestAttempt: { executionId: "s2", status: "RUNNING", attemptNumber: 2 },
    });
    await postgres.query(
      "UPDATE step_execution SET status='COMPLETED',ended_at=now() WHERE id='s2'; UPDATE workflow_instance SET status='FAILED';",
    );
    const list = await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .expect(200);
    expect(list.body.items[0]).toMatchObject({
      id: "s",
      historical: true,
      latestAttempt: { status: "COMPLETED" },
    });
    await postgres.query("UPDATE workflow_instance SET status='CANCELLED';");
    await request(app.getHttpServer()).get("/api/v1/incidents/s").expect(404);
  });

  it("fails readiness clearly for missing new schema fields and recovers", async () => {
    await postgres.query(
      "ALTER TABLE job RENAME COLUMN lock_expires_at TO hidden_expiry;",
    );
    try {
      await request(app.getHttpServer()).get("/health/ready").expect(503);
    } finally {
      await postgres.query(
        "ALTER TABLE job RENAME COLUMN hidden_expiry TO lock_expires_at;",
      );
    }
    await request(app.getHttpServer()).get("/health/ready").expect(200);
    await postgres.query(
      "REVOKE SELECT ON user_task FROM rochallor_monitor_test;",
    );
    try {
      await request(app.getHttpServer()).get("/health/ready").expect(503);
    } finally {
      await postgres.query(
        "GRANT SELECT ON user_task TO rochallor_monitor_test;",
      );
    }
  });
});
