import type { INestApplication } from "@nestjs/common";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Step Execution history HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;

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
        started_at
      ) VALUES
        (
          'history-instance',
          'loan-approval',
          1,
          'WAITING',
          ARRAY['check-risk'],
          '{}',
          '2026-04-01T00:00:00Z'
        ),
        (
          'empty-history-instance',
          'loan-approval',
          1,
          'ACTIVE',
          '{}',
          '{}',
          '2026-04-02T00:00:00Z'
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
        input_snapshot,
        output_snapshot,
        failure_reason
      ) VALUES
        (
          'risk-attempt-1',
          'history-instance',
          'check-risk',
          'SERVICE_TASK',
          1,
          'FAILED',
          '2026-04-01T00:00:01Z',
          '2026-04-01T00:00:02Z',
          '{"ssn":"sensitive-input"}',
          NULL,
          'worker failed'
        ),
        (
          'risk-attempt-2',
          'history-instance',
          'check-risk',
          'SERVICE_TASK',
          2,
          'RUNNING',
          '2026-04-01T00:00:03Z',
          NULL,
          '{"ssn":"sensitive-retry-input"}',
          '{"score":720}',
          NULL
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

  it("returns every attempt separately without embedding snapshot content", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances/history-instance/step-executions")
      .expect(200)
      .expect({
        items: [
          {
            id: "risk-attempt-2",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 2,
            status: "RUNNING",
            startedAt: "2026-04-01T00:00:03.000Z",
            endedAt: null,
            hasFailure: false,
            hasInputSnapshot: true,
            hasOutputSnapshot: true,
          },
          {
            id: "risk-attempt-1",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 1,
            status: "FAILED",
            startedAt: "2026-04-01T00:00:01.000Z",
            endedAt: "2026-04-01T00:00:02.000Z",
            hasFailure: true,
            hasInputSnapshot: true,
            hasOutputSnapshot: false,
          },
        ],
        nextCursor: null,
      });
  });

  it("continues Step Execution history through an opaque cursor", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const firstPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances/history-instance/step-executions")
      .query({ pageSize: 1 })
      .expect(200);

    expect(firstPage.body.items.map((item: { id: string }) => item.id)).toEqual(
      ["risk-attempt-2"],
    );
    expect(firstPage.body.nextCursor).toEqual(expect.any(String));

    const secondPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances/history-instance/step-executions")
      .query({ pageSize: 1, cursor: firstPage.body.nextCursor })
      .expect(200);

    expect(
      secondPage.body.items.map((item: { id: string }) => item.id),
    ).toEqual(["risk-attempt-1"]);
    expect(secondPage.body.nextCursor).toBeNull();
  });

  it("rejects a malformed Step Execution cursor", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances/history-instance/step-executions")
      .query({ cursor: "not-a-valid-cursor" })
      .expect(400);
  });

  it("returns an empty history for a Process Instance with no attempts", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances/empty-history-instance/step-executions")
      .expect(200)
      .expect({ items: [], nextCursor: null });
  });

  it("orders equal-timestamp attempts stably across pages", async () => {
    if (!app || !postgres) {
      throw new Error("BFF app did not start");
    }

    await postgres.query(`
      INSERT INTO step_execution (
        id,
        instance_id,
        step_id,
        step_type,
        attempt_number,
        status,
        started_at
      ) VALUES
        (
          'equal-attempt-a',
          'history-instance',
          'manual-review',
          'USER_TASK',
          1,
          'COMPLETED',
          '2026-04-01T00:00:04Z'
        ),
        (
          'equal-attempt-b',
          'history-instance',
          'final-review',
          'USER_TASK',
          1,
          'COMPLETED',
          '2026-04-01T00:00:04Z'
        )
    `);

    try {
      const firstPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances/history-instance/step-executions")
        .query({ pageSize: 1 })
        .expect(200);
      const secondPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances/history-instance/step-executions")
        .query({ pageSize: 1, cursor: firstPage.body.nextCursor })
        .expect(200);

      expect(firstPage.body.items[0].id).toBe("equal-attempt-b");
      expect(secondPage.body.items[0].id).toBe("equal-attempt-a");
    } finally {
      await postgres.query(`
        DELETE FROM step_execution
        WHERE id IN ('equal-attempt-a', 'equal-attempt-b')
      `);
    }
  });

  it("does not expose Step Execution mutation operations", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .post(
        "/api/v1/process-instances/history-instance/step-executions/risk-attempt-1/retry",
      )
      .expect(404);
  });
});
