import type { INestApplication } from "@nestjs/common";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Process Variable HTTP seam", () => {
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
          'variables-instance',
          'loan-approval',
          1,
          'WAITING',
          ARRAY['check-risk'],
          '{
            "applicant":"Ada",
            "approved":false,
            "score":720,
            "notes":null,
            "profile":{"country":"VN"},
            "tags":["priority",3]
          }',
          '2026-05-01T00:00:00Z'
        ),
        (
          'foreign-instance',
          'loan-approval',
          1,
          'COMPLETED',
          '{}',
          '{"secret":"foreign"}',
          '2026-05-02T00:00:00Z'
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
        output_snapshot
      ) VALUES
        (
          'risk-delta-execution',
          'variables-instance',
          'check-risk',
          'SERVICE_TASK',
          1,
          'COMPLETED',
          '2026-05-01T00:00:01Z',
          '2026-05-01T00:00:02Z',
          '{"applicant":"Ada","notes":null}',
          '{"riskScore":720}'
        ),
        (
          'null-input-execution',
          'variables-instance',
          'notify-applicant',
          'SERVICE_TASK',
          1,
          'COMPLETED',
          '2026-05-01T00:00:03Z',
          '2026-05-01T00:00:04Z',
          'null'::jsonb,
          NULL
        ),
        (
          'merged-output-execution',
          'variables-instance',
          'merge-risk',
          'TRANSFORMATION',
          1,
          'COMPLETED',
          '2026-05-01T00:00:05Z',
          '2026-05-01T00:00:06Z',
          '{"applicant":"Ada"}',
          '{"applicant":"Ada","riskScore":720}'
        ),
        (
          'foreign-execution',
          'foreign-instance',
          'check-risk',
          'SERVICE_TASK',
          1,
          'COMPLETED',
          '2026-05-02T00:00:01Z',
          '2026-05-02T00:00:02Z',
          '{"secret":"foreign-input"}',
          '{"secret":"foreign-output"}'
        ),
        (
          'oversized-execution',
          'variables-instance',
          'archive-record',
          'SERVICE_TASK',
          1,
          'COMPLETED',
          '2026-05-01T00:00:07Z',
          '2026-05-01T00:00:08Z',
          '{"ok":true}',
          '{"payload":"abcdefghijklmnopqrstuvwxyz"}'
        )
    `);

    const originalLimit = process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES;
    Reflect.deleteProperty(process.env, "MONITOR_MAX_JSON_DOCUMENT_BYTES");
    app = await createMonitorApp({
      postgresDsn: postgres.readOnlyDsn,
      log: () => undefined,
    });
    if (originalLimit !== undefined) {
      process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES = originalLimit;
    }
    await app.init();
  }, 30_000);

  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  }, 30_000);

  it("returns authoritative Current Variables with JSON types intact", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get("/api/v1/process-instances/variables-instance/variables")
      .expect(200);

    expect(response.body).toEqual({
      current: {
        status: "present",
        value: {
          applicant: "Ada",
          approved: false,
          score: 720,
          notes: null,
          profile: { country: "VN" },
          tags: ["priority", 3],
        },
        sizeBytes: expect.any(Number),
      },
    });
    expect(response.body.current.sizeBytes).toBeGreaterThan(0);
  });

  it("returns Recorded Input and delta Recorded Output independently", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get(
        "/api/v1/process-instances/variables-instance/step-executions/risk-delta-execution/variables",
      )
      .expect(200);

    expect(response.body).toEqual({
      recordedInput: {
        status: "present",
        value: { applicant: "Ada", notes: null },
        sizeBytes: expect.any(Number),
      },
      recordedOutput: {
        status: "present",
        value: { riskScore: 720 },
        sizeBytes: expect.any(Number),
      },
    });
  });

  it("distinguishes a recorded JSON null from a snapshot that was not recorded", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get(
        "/api/v1/process-instances/variables-instance/step-executions/null-input-execution/variables",
      )
      .expect(200)
      .expect({
        recordedInput: {
          status: "present",
          value: null,
          sizeBytes: 4,
        },
        recordedOutput: {
          status: "notRecorded",
        },
      });
  });

  it("preserves merged Recorded Output separately from service-task delta output", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    const response = await request(app.getHttpServer())
      .get(
        "/api/v1/process-instances/variables-instance/step-executions/merged-output-execution/variables",
      )
      .expect(200);

    expect(response.body.recordedOutput).toEqual({
      status: "present",
      value: { applicant: "Ada", riskScore: 720 },
      sizeBytes: expect.any(Number),
    });
  });

  it("returns 404 when a Step Execution belongs to another Process Instance", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get(
        "/api/v1/process-instances/variables-instance/step-executions/foreign-execution/variables",
      )
      .expect(404);
  });

  it("omits oversized snapshot content using the configured environment limit", async () => {
    if (!postgres) {
      throw new Error("PostgreSQL fixture did not start");
    }

    const originalLimit = process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES;
    process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES = "32";
    const limitedApp = await createMonitorApp({
      postgresDsn: postgres.readOnlyDsn,
      log: () => undefined,
    });
    if (originalLimit === undefined) {
      Reflect.deleteProperty(process.env, "MONITOR_MAX_JSON_DOCUMENT_BYTES");
    } else {
      process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES = originalLimit;
    }
    await limitedApp.init();

    try {
      await request(limitedApp.getHttpServer())
        .get(
          "/api/v1/process-instances/variables-instance/step-executions/oversized-execution/variables",
        )
        .expect(200)
        .expect({
          recordedInput: {
            status: "present",
            value: { ok: true },
            sizeBytes: 11,
          },
          recordedOutput: {
            status: "contentTooLarge",
            sizeBytes: 40,
          },
        });
    } finally {
      await limitedApp.close();
    }
  });

  it("defaults the Current Variables document limit to 5 MiB", async () => {
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
      ) VALUES (
        'default-oversized-instance',
        'loan-approval',
        1,
        'ACTIVE',
        '{}',
        jsonb_build_object('payload', repeat('x', 5 * 1024 * 1024)),
        '2026-05-03T00:00:00Z'
      )
    `);

    try {
      await request(app.getHttpServer())
        .get("/api/v1/process-instances/default-oversized-instance/variables")
        .expect(200)
        .expect({
          current: {
            status: "contentTooLarge",
            sizeBytes: 5_242_894,
          },
        });
    } finally {
      await postgres.query(`
        DELETE FROM workflow_instance
        WHERE id = 'default-oversized-instance'
      `);
    }
  });
});
