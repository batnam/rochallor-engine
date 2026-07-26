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
    const admin = new Pool({ connectionString: postgres.dsn });
    await admin.query("ALTER TABLE workflow_instance DROP COLUMN status");
    await admin.end();

    app = await createMonitorApp({ postgresDsn: postgres.readOnlyDsn });
    await app.init();
  });

  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });

  it("reports unavailable when a required column is missing", async () => {
    if (!app) {
      throw new Error("BFF app did not start");
    }

    await request(app.getHttpServer())
      .get("/health/ready")
      .expect(503)
      .expect({ status: "unavailable" });
  });
});
