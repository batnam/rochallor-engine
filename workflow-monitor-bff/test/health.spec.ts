import type { INestApplication } from "@nestjs/common";
import { Pool } from "pg";
import request from "supertest";

import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("liveness HTTP seam", () => {
  let app: INestApplication;

  beforeAll(async () => {
    app = await createMonitorApp();
    await app.init();
  });

  afterAll(async () => {
    await app.close();
  });

  it("reports that the BFF process is alive", async () => {
    await request(app.getHttpServer())
      .get("/health/live")
      .expect(200)
      .expect({ status: "ok" });
  });
});

describe("readiness HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    app = await createMonitorApp({ postgresDsn: postgres.readOnlyDsn });
    await app.init();
  });

  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });

  it("reports ready when the engine-owned schema is available", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/health/ready")
      .expect(200)
      .expect({ status: "ok" });
  });
});

describe("schema compatibility HTTP seam", () => {
  let app: INestApplication | undefined;
  let postgres: PostgresFixture | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    app = await createMonitorApp({ postgresDsn: postgres.readOnlyDsn });
    await app.init();
  });

  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });

  it.each([
    ["workflow_instance", "status"],
    ["workflow_instance", "variables"],
    ["workflow_definition", "raw_json"],
    ["step_execution", "output_snapshot"],
    ["job", "job_type"],
  ])("reports unavailable when %s.%s is missing", async (table, column) => {
    if (!app) {
      throw new Error("BFF app did not start");
    }
    const admin = new Pool({ connectionString: postgres?.dsn });
    try {
      await admin.query(
        `ALTER TABLE ${table} RENAME COLUMN ${column} TO missing_column`,
      );
      await request(app.getHttpServer())
        .get("/health/ready")
        .expect(503)
        .expect({ status: "unavailable" });
    } finally {
      await admin.query(
        `ALTER TABLE ${table} RENAME COLUMN missing_column TO ${column}`,
      );
      await admin.end();
    }
  });

  it("rejects an incompatible column type even when all columns exist", async () => {
    const admin = new Pool({ connectionString: postgres?.dsn });
    try {
      await admin.query(
        "ALTER TABLE workflow_definition ALTER COLUMN raw_json TYPE text",
      );
      await request(app?.getHttpServer()).get("/health/ready").expect(503);
    } finally {
      await admin.query(
        "ALTER TABLE workflow_definition ALTER COLUMN raw_json TYPE jsonb USING raw_json::jsonb",
      );
      await admin.end();
    }
  });

  it("fails startup with a diagnostic before serving an incompatible schema", async () => {
    const admin = new Pool({ connectionString: postgres?.dsn });
    let incompatibleApp: INestApplication | undefined;
    try {
      await admin.query(
        "ALTER TABLE job RENAME COLUMN job_type TO missing_column",
      );
      incompatibleApp = await createMonitorApp({
        postgresDsn: postgres?.readOnlyDsn,
      });
      await expect(incompatibleApp.init()).rejects.toThrow(
        /Monitor schema.*job/i,
      );
    } finally {
      await incompatibleApp?.close();
      await admin.query(
        "ALTER TABLE job RENAME COLUMN missing_column TO job_type",
      );
      await admin.end();
    }
  });
});
