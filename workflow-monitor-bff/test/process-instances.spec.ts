import type { INestApplication } from "@nestjs/common";
import { Pool } from "pg";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Process Instance list HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;
  const logs: unknown[] = [];

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    const admin = new Pool({ connectionString: postgres.dsn });
    await admin.query(`
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
          'instance-active',
          'loan-approval',
          1,
          'ACTIVE',
          ARRAY['collect-documents'],
          '{"applicant":"Ada"}',
          '2026-01-01T00:00:00Z',
          NULL,
          NULL,
          'loan-001'
        ),
        (
          'instance-failed',
          'loan-approval',
          2,
          'FAILED',
          ARRAY['check-risk'],
          '{"applicant":"Grace"}',
          '2026-01-02T00:00:00Z',
          '2026-01-02T00:05:00Z',
          'worker failed',
          'loan-002'
        )
    `);
    await admin.query(`
      INSERT INTO workflow_definition (
        id,
        version,
        name,
        raw_json,
        parsed_steps
      ) VALUES
        ('loan-approval', 1, 'Loan Approval v1', '{}', '[]'),
        ('loan-approval', 2, 'Loan Approval', '{}', '[]'),
        ('payment', 1, 'Payment', '{}', '[]')
    `);
    await admin.end();

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

  it("lists Process Instances from PostgreSQL without an engine process", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .expect(200)
      .expect({
        items: [
          {
            id: "instance-failed",
            definitionId: "loan-approval",
            definitionVersion: 2,
            status: "FAILED",
            currentStepIds: ["check-risk"],
            startedAt: "2026-01-02T00:00:00.000Z",
            completedAt: "2026-01-02T00:05:00.000Z",
            failureReason: "worker failed",
            businessKey: "loan-002",
          },
          {
            id: "instance-active",
            definitionId: "loan-approval",
            definitionVersion: 1,
            status: "ACTIVE",
            currentStepIds: ["collect-documents"],
            startedAt: "2026-01-01T00:00:00.000Z",
            completedAt: null,
            failureReason: null,
            businessKey: "loan-001",
          },
        ],
        nextCursor: null,
      });
  });

  it("filters Process Instances by exact definition, status, business key, and start time", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({
        definitionId: "loan-approval",
        status: "FAILED",
        businessKey: "loan-002",
        from: "2026-01-02T00:00:00Z",
        to: "2026-01-03T00:00:00Z",
      })
      .expect(200);

    expect(response.body.items).toHaveLength(1);
    expect(response.body.items[0]).toEqual(
      expect.objectContaining({
        id: "instance-failed",
        definitionId: "loan-approval",
        status: "FAILED",
        businessKey: "loan-002",
      }),
    );
  });

  it("accepts multiple statuses and applies an inclusive-from, exclusive-to range", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({
        status: ["ACTIVE", "FAILED"],
        from: "2026-01-01T00:00:00Z",
        to: "2026-01-02T00:00:00Z",
      })
      .expect(200);

    expect(response.body.items.map((item: { id: string }) => item.id)).toEqual([
      "instance-active",
    ]);
  });

  it("rejects an unknown Process Instance status", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ status: "PAUSED" })
      .expect(400);
  });

  it("rejects an empty Process Instance status", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances?status=")
      .expect(400);
  });

  it("rejects a malformed UTC start-time bound", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ from: "yesterday" })
      .expect(400);
  });

  it("rejects a UTC start-time bound with a nonexistent calendar date", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ from: "2026-02-31T00:00:00Z" })
      .expect(400);
  });

  it("rejects a reversed start-time range", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({
        from: "2026-01-03T00:00:00Z",
        to: "2026-01-02T00:00:00Z",
      })
      .expect(400);
  });

  it("continues a stable descending list through an opaque cursor", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const firstPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ pageSize: 1 })
      .expect(200);

    expect(firstPage.body.items.map((item: { id: string }) => item.id)).toEqual(
      ["instance-failed"],
    );
    expect(firstPage.body.nextCursor).toEqual(expect.any(String));

    const secondPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ pageSize: 1, cursor: firstPage.body.nextCursor })
      .expect(200);

    expect(
      secondPage.body.items.map((item: { id: string }) => item.id),
    ).toEqual(["instance-active"]);
    expect(secondPage.body.nextCursor).toBeNull();
  });

  it("rejects a malformed cursor", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ cursor: "not-a-valid-cursor" })
      .expect(400);
  });

  it("rejects a cursor containing non-Base64URL characters", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const firstPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ pageSize: 1 })
      .expect(200);

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ cursor: `${firstPage.body.nextCursor}!` })
      .expect(400);
  });

  it("rejects a cursor reused with different filters", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const firstPage = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ pageSize: 1 })
      .expect(200);

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({
        pageSize: 1,
        status: "ACTIVE",
        cursor: firstPage.body.nextCursor,
      })
      .expect(400);
  });

  it("does not duplicate or omit equal-timestamp rows when a newer row is inserted", async () => {
    if (!app || !postgres) {
      throw new Error("BFF app did not start");
    }

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
        ('instance-same-a', 'cursor-test', 1, 'ACTIVE', '{}', '{}', '2026-01-03T00:00:00Z'),
        ('instance-same-b', 'cursor-test', 1, 'ACTIVE', '{}', '{}', '2026-01-03T00:00:00Z')
    `);

    try {
      const firstPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ definitionId: "cursor-test", pageSize: 1 })
        .expect(200);
      expect(firstPage.body.items[0].id).toBe("instance-same-b");

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
          ('instance-concurrent', 'cursor-test', 1, 'ACTIVE', '{}', '{}', '2026-01-04T00:00:00Z')
      `);

      const secondPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({
          definitionId: "cursor-test",
          pageSize: 1,
          cursor: firstPage.body.nextCursor,
        })
        .expect(200);

      expect(
        secondPage.body.items.map((item: { id: string }) => item.id),
      ).toEqual(["instance-same-a"]);
      expect(secondPage.body.nextCursor).toBeNull();
    } finally {
      await postgres.query(
        "DELETE FROM workflow_instance WHERE definition_id = 'cursor-test'",
      );
    }
  });

  it("defaults to 50 items and enforces a maximum page size of 100", async () => {
    if (!app || !postgres) {
      throw new Error("BFF app did not start");
    }

    await postgres.query(`
      INSERT INTO workflow_instance (
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        variables,
        started_at
      )
      SELECT
        'bulk-' || series,
        'bulk-test',
        1,
        'COMPLETED',
        '{}',
        '{}',
        '2026-02-01T00:00:00Z'::timestamptz - series * interval '1 second'
      FROM generate_series(1, 51) AS series
    `);

    try {
      const defaultPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ definitionId: "bulk-test" })
        .expect(200);
      expect(defaultPage.body.items).toHaveLength(50);
      expect(defaultPage.body.nextCursor).toEqual(expect.any(String));

      const maximumPage = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ definitionId: "bulk-test", pageSize: 100 })
        .expect(200);
      expect(maximumPage.body.items).toHaveLength(51);
      expect(maximumPage.body.nextCursor).toBeNull();

      await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ pageSize: 101 })
        .expect(400);
    } finally {
      await postgres.query(
        "DELETE FROM workflow_instance WHERE definition_id = 'bulk-test'",
      );
    }
  });

  it("lists one latest option per Workflow Definition", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/api/v1/workflow-definitions")
      .expect(200)
      .expect({
        items: [
          { id: "loan-approval", name: "Loan Approval" },
          { id: "payment", name: "Payment" },
        ],
      });
  });

  it("logs request metadata without workflow data", async () => {
    if (!app || !postgres) {
      throw new Error("BFF app did not start");
    }
    logs.length = 0;

    await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .set("X-Request-ID", "request-123")
      .expect(200)
      .expect("X-Request-ID", "request-123");

    expect(logs).toEqual([
      expect.objectContaining({
        event: "http_request",
        requestId: "request-123",
        route: "/api/v1/process-instances",
        status: 200,
        durationMs: expect.any(Number),
      }),
    ]);

    const serializedLogs = JSON.stringify(logs);
    expect(serializedLogs).not.toContain(postgres.dsn);
    expect(serializedLogs).not.toContain("Ada");
    expect(serializedLogs).not.toContain("Grace");
    expect(serializedLogs).not.toContain("worker failed");
    expect(serializedLogs).not.toContain("loan-002");
  });
});
