import type { INestApplication } from "@nestjs/common";
import { Pool } from "pg";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Process Instance detail HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_definition (
        id,
        version,
        name,
        raw_json,
        parsed_steps
      ) VALUES
        (
          'parallel-review',
          1,
          'Parallel Review v1',
          '{
            "id":"parallel-review",
            "name":"Parallel Review v1",
            "steps":[
              {"id":"collect-documents","name":"Collect Documents","type":"USER_TASK","nextStep":"join"},
              {"id":"manual-review","name":"Manual Review","type":"USER_TASK","nextStep":"join"},
              {"id":"join","name":"Join","type":"JOIN_GATEWAY","nextStep":"end"},
              {"id":"end","name":"End","type":"END"}
            ]
          }',
          '[]'
        ),
        (
          'parallel-review',
          2,
          'Parallel Review v2',
          '{
            "id":"parallel-review",
            "name":"Parallel Review v2",
            "steps":[
              {"id":"replacement-step","name":"Replacement","type":"SERVICE_TASK","nextStep":"end"},
              {"id":"end","name":"End","type":"END"}
            ]
          }',
          '[]'
        );

      INSERT INTO workflow_instance (
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        variables,
        started_at,
        business_key
      ) VALUES
        (
          'parallel-active',
          'parallel-review',
          1,
          'ACTIVE',
          ARRAY['collect-documents', 'manual-review'],
          '{}',
          '2026-03-01T00:00:00Z',
          'review-001'
        ),
        (
          'retry-waiting',
          'parallel-review',
          1,
          'WAITING',
          ARRAY['collect-documents'],
          '{}',
          '2026-03-02T00:00:00Z',
          'review-002'
        ),
        (
          'review-failed',
          'parallel-review',
          1,
          'FAILED',
          ARRAY['manual-review'],
          '{}',
          '2026-03-03T00:00:00Z',
          'review-003'
        ),
        (
          'review-completed',
          'parallel-review',
          1,
          'COMPLETED',
          ARRAY['end'],
          '{}',
          '2026-03-04T00:00:00Z',
          'review-004'
        ),
        (
          'review-cancelled',
          'parallel-review',
          1,
          'CANCELLED',
          ARRAY['manual-review'],
          '{}',
          '2026-03-05T00:00:00Z',
          'review-005'
        ),
        (
          'consistent-active',
          'parallel-review',
          1,
          'ACTIVE',
          ARRAY['collect-documents'],
          '{}',
          '2026-03-06T00:00:00Z',
          'review-006'
        );

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
      ) VALUES
        (
          'collect-attempt-1',
          'retry-waiting',
          'collect-documents',
          'USER_TASK',
          1,
          'FAILED',
          '2026-03-02T00:00:01Z',
          '2026-03-02T00:00:02Z',
          'first attempt failed'
        ),
        (
          'collect-attempt-2',
          'retry-waiting',
          'collect-documents',
          'USER_TASK',
          2,
          'RUNNING',
          '2026-03-02T00:00:03Z',
          NULL,
          NULL
        ),
        (
          'failed-collect',
          'review-failed',
          'collect-documents',
          'USER_TASK',
          1,
          'FAILED',
          '2026-03-03T00:00:01Z',
          '2026-03-03T00:00:02Z',
          'collection failed'
        ),
        (
          'failed-manual',
          'review-failed',
          'manual-review',
          'USER_TASK',
          1,
          'FAILED',
          '2026-03-03T00:00:03Z',
          '2026-03-03T00:00:04Z',
          'manual review failed'
        );
    `);

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

  it("returns the exact Workflow Definition version and every Current Token Position", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances/parallel-active")
      .expect(200)
      .expect({
        instance: {
          id: "parallel-active",
          definitionId: "parallel-review",
          definitionVersion: 1,
          status: "ACTIVE",
          currentStepIds: ["collect-documents", "manual-review"],
          startedAt: "2026-03-01T00:00:00.000Z",
          completedAt: null,
          failureReason: null,
          businessKey: "review-001",
        },
        definition: {
          id: "parallel-review",
          version: 1,
          name: "Parallel Review v1",
          steps: [
            {
              id: "collect-documents",
              name: "Collect Documents",
              type: "USER_TASK",
              nextStep: "join",
            },
            {
              id: "manual-review",
              name: "Manual Review",
              type: "USER_TASK",
              nextStep: "join",
            },
            {
              id: "join",
              name: "Join",
              type: "JOIN_GATEWAY",
              nextStep: "end",
            },
            { id: "end", name: "End", type: "END" },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: ["collect-documents", "manual-review"],
          failedStepId: null,
          latestByStep: [],
        },
      });
  });

  it("uses the latest attempt for each step in the execution overlay", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances/retry-waiting")
      .expect(200);

    expect(response.body.executionOverlay).toEqual({
      currentTokenStepIds: ["collect-documents"],
      failedStepId: null,
      latestByStep: [
        {
          executionId: "collect-attempt-2",
          stepId: "collect-documents",
          status: "RUNNING",
          attemptNumber: 2,
        },
      ],
    });
  });

  it("marks the latest failed Step Execution instead of showing a running token", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances/review-failed")
      .expect(200);

    expect(response.body.executionOverlay.currentTokenStepIds).toEqual([]);
    expect(response.body.executionOverlay.failedStepId).toBe("manual-review");
  });

  it("shows no Current Token Position for completed or cancelled instances", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const completed = await request(app.getHttpServer())
      .get("/api/v1/process-instances/review-completed")
      .expect(200);
    const cancelled = await request(app.getHttpServer())
      .get("/api/v1/process-instances/review-cancelled")
      .expect(200);

    expect(completed.body.executionOverlay.currentTokenStepIds).toEqual([]);
    expect(cancelled.body.executionOverlay.currentTokenStepIds).toEqual([]);
  });

  it("does not mix a concurrent engine-style update into one detail response", async () => {
    if (!app || !postgres) {
      throw new Error("BFF app did not start");
    }

    const lockerPool = new Pool({ connectionString: postgres.dsn });
    const observerPool = new Pool({ connectionString: postgres.dsn });
    const locker = await lockerPool.connect();
    try {
      await locker.query("BEGIN");
      await locker.query(
        "LOCK TABLE workflow_definition IN ACCESS EXCLUSIVE MODE",
      );

      const responsePromise = request(app.getHttpServer())
        .get("/api/v1/process-instances/consistent-active")
        .expect(200)
        .then((response) => response);

      let detailQueryIsBlocked = false;
      for (let attempt = 0; attempt < 100; attempt += 1) {
        const activity = await observerPool.query<{ blocked: boolean }>(`
          SELECT EXISTS (
            SELECT 1
            FROM pg_stat_activity
            WHERE usename = 'rochallor_monitor_test'
              AND wait_event_type = 'Lock'
              AND query LIKE '%FROM workflow_definition%'
          ) AS blocked
        `);
        if (activity.rows[0].blocked) {
          detailQueryIsBlocked = true;
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      expect(detailQueryIsBlocked).toBe(true);

      await postgres.query(`
        UPDATE workflow_instance
        SET status = 'CANCELLED', current_step_ids = '{}'
        WHERE id = 'consistent-active';

        INSERT INTO step_execution (
          id,
          instance_id,
          step_id,
          step_type,
          attempt_number,
          status,
          started_at,
          ended_at
        ) VALUES (
          'concurrent-execution',
          'consistent-active',
          'collect-documents',
          'USER_TASK',
          1,
          'COMPLETED',
          '2026-03-06T00:00:01Z',
          '2026-03-06T00:00:02Z'
        );
      `);
      await locker.query("COMMIT");

      const response = await responsePromise;
      expect(response.body.instance.status).toBe("ACTIVE");
      expect(response.body.executionOverlay.currentTokenStepIds).toEqual([
        "collect-documents",
      ]);
      expect(response.body.executionOverlay.latestByStep).toEqual([]);
    } finally {
      await locker.query("ROLLBACK");
      locker.release();
      await lockerPool.end();
      await observerPool.end();
    }
  });
});
