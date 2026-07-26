import type { INestApplication } from "@nestjs/common";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Incident HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;
  const logs: unknown[] = [];

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
        ('loan-approval', 1, 'Loan Approval', '{}', '[]'),
        ('account-review', 2, 'Account Review', '{}', '[]');

      INSERT INTO workflow_instance (
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        variables,
        started_at,
        completed_at,
        failure_reason,
        business_key
      ) VALUES
        (
          'instance-service',
          'loan-approval',
          1,
          'FAILED',
          '{}',
          '{}',
          '2026-03-01T00:00:00Z',
          '2026-03-01T00:03:00Z',
          'instance context only',
          'loan-001'
        ),
        (
          'instance-script',
          'account-review',
          2,
          'FAILED',
          '{}',
          '{}',
          '2026-03-01T00:00:00Z',
          '2026-03-01T00:02:00Z',
          NULL,
          'account-001'
        ),
        (
          'instance-cancelled',
          'loan-approval',
          1,
          'CANCELLED',
          '{}',
          '{}',
          '2026-03-01T00:00:00Z',
          '2026-03-01T00:04:00Z',
          NULL,
          NULL
        ),
        (
          'instance-only',
          'loan-approval',
          1,
          'FAILED',
          '{}',
          '{}',
          '2026-03-01T00:00:00Z',
          '2026-03-01T00:05:00Z',
          'no failed step',
          NULL
        ),
        (
          'instance-job-only',
          'loan-approval',
          1,
          'ACTIVE',
          '{}',
          '{}',
          '2026-03-01T00:00:00Z',
          NULL,
          NULL,
          NULL
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
          'execution-service',
          'instance-service',
          'charge-card',
          'SERVICE_TASK',
          2,
          'FAILED',
          '2026-03-01T00:01:00Z',
          '2026-03-01T00:03:00Z',
          'card processor unavailable'
        ),
        (
          'execution-script',
          'instance-script',
          'validate-account',
          'SCRIPT_TASK',
          1,
          'FAILED',
          '2026-03-01T00:01:00Z',
          '2026-03-01T00:02:00Z',
          'validation returned false'
        ),
        (
          'execution-cancelled',
          'instance-cancelled',
          'charge-card',
          'SERVICE_TASK',
          1,
          'FAILED',
          '2026-03-01T00:01:00Z',
          '2026-03-01T00:04:00Z',
          'cancelled failure'
        ),
        (
          'execution-job-only',
          'instance-job-only',
          'send-email',
          'SERVICE_TASK',
          1,
          'COMPLETED',
          '2026-03-01T00:01:00Z',
          '2026-03-01T00:05:00Z',
          NULL
        );

      INSERT INTO job (
        id,
        instance_id,
        step_execution_id,
        job_type,
        status,
        worker_id,
        retries_remaining
      ) VALUES
        (
          'job-service',
          'instance-service',
          'execution-service',
          'payments',
          'FAILED',
          'worker-1',
          0
        ),
        (
          'job-only',
          'instance-job-only',
          'execution-job-only',
          'notifications',
          'FAILED',
          'worker-2',
          0
        );
    `);

    app = await createMonitorApp({
      postgresDsn: postgres.readOnlyDsn,
      log: (record) => logs.push(record),
    });
    await app.init();
  });

  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });

  it("lists each canonical failed Step Execution exactly once", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .expect(200)
      .expect({
        items: [
          {
            id: "execution-service",
            processInstanceId: "instance-service",
            definitionId: "loan-approval",
            definitionVersion: 1,
            definitionName: "Loan Approval",
            stepId: "charge-card",
            stepType: "SERVICE_TASK",
            attemptNumber: 2,
            occurredAt: "2026-03-01T00:03:00.000Z",
            job: {
              id: "job-service",
              type: "payments",
              status: "FAILED",
            },
          },
          {
            id: "execution-script",
            processInstanceId: "instance-script",
            definitionId: "account-review",
            definitionVersion: 2,
            definitionName: "Account Review",
            stepId: "validate-account",
            stepType: "SCRIPT_TASK",
            attemptNumber: 1,
            occurredAt: "2026-03-01T00:02:00.000Z",
            job: null,
          },
        ],
        nextCursor: null,
      });
  });

  it("filters by exact definition, exact job type, and occurrence range", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const included = await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .query({
        definitionId: "loan-approval",
        jobType: "payments",
        from: "2026-03-01T00:03:00Z",
        to: "2026-03-01T00:04:00Z",
      })
      .expect(200);
    expect(included.body.items.map((item: { id: string }) => item.id)).toEqual([
      "execution-service",
    ]);

    const excludedAtUpperBound = await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .query({ to: "2026-03-01T00:03:00Z" })
      .expect(200);
    expect(
      excludedAtUpperBound.body.items.map((item: { id: string }) => item.id),
    ).toEqual(["execution-script"]);
  });

  it("continues a stable Incident list through a filter-bound opaque cursor", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const firstPage = await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .query({ pageSize: 1 })
      .expect(200);
    expect(firstPage.body.items.map((item: { id: string }) => item.id)).toEqual(
      ["execution-service"],
    );
    expect(firstPage.body.nextCursor).toEqual(expect.any(String));

    const secondPage = await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .query({ pageSize: 1, cursor: firstPage.body.nextCursor })
      .expect(200);
    expect(
      secondPage.body.items.map((item: { id: string }) => item.id),
    ).toEqual(["execution-script"]);
    expect(secondPage.body.nextCursor).toBeNull();

    await request(app.getHttpServer())
      .get("/api/v1/incidents")
      .query({
        definitionId: "loan-approval",
        pageSize: 1,
        cursor: firstPage.body.nextCursor,
      })
      .expect(400);
  });

  it("returns persisted Error Details and context without logging the error", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }
    logs.length = 0;

    await request(app.getHttpServer())
      .get("/api/v1/incidents/execution-service")
      .set("X-Request-ID", "incident-detail-request")
      .expect(200)
      .expect({
        incident: {
          id: "execution-service",
          processInstanceId: "instance-service",
          definitionId: "loan-approval",
          definitionVersion: 1,
          definitionName: "Loan Approval",
          stepId: "charge-card",
          stepType: "SERVICE_TASK",
          attemptNumber: 2,
          occurredAt: "2026-03-01T00:03:00.000Z",
          errorDetails: "card processor unavailable",
          processInstance: {
            id: "instance-service",
            status: "FAILED",
            businessKey: "loan-001",
          },
          job: {
            id: "job-service",
            type: "payments",
            status: "FAILED",
          },
        },
      });

    expect(JSON.stringify(logs)).not.toContain("card processor unavailable");
  });
});
