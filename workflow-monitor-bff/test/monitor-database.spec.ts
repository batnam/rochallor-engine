import { Pool } from "pg";

import { MonitorDatabase } from "../src/common/database/monitor-database";
import { ProcessInstanceQueries } from "../src/modules/process-instances/process-instance.queries";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Monitor database resource limits", () => {
  let postgres: PostgresFixture;
  let database: MonitorDatabase;
  const originalEnvironment = { ...process.env };

  beforeAll(async () => {
    postgres = await startPostgresFixture();
  });
  beforeEach(() => {
    process.env.MONITOR_POSTGRES_POOL_MAX = "1";
    process.env.MONITOR_POSTGRES_CONNECTION_TIMEOUT_MS = "100";
    process.env.MONITOR_POSTGRES_STATEMENT_TIMEOUT_MS = "100";
    database = new MonitorDatabase(postgres.readOnlyDsn);
  });
  afterEach(async () => {
    await database.onApplicationShutdown();
    process.env = { ...originalEnvironment };
  });
  afterAll(async () => postgres?.stop());

  it("cancels slow SQL on the server and recovers for the next request", async () => {
    await expect(database.query("SELECT pg_sleep(0.5)")).rejects.toMatchObject({
      code: "57014",
    });
    await expect(database.query("SELECT 1 AS value")).resolves.toMatchObject({
      rows: [{ value: 1 }],
    });
  });

  it("bounds the wait for a saturated pool and recovers after release", async () => {
    const held = await database.connect();
    try {
      await expect(database.query("SELECT 1")).rejects.toThrow(/timeout/i);
    } finally {
      held.release();
    }
    await expect(database.query("SELECT 1 AS value")).resolves.toMatchObject({
      rows: [{ value: 1 }],
    });
  });

  it("rolls back a timed-out transaction before returning the connection", async () => {
    const admin = new Pool({ connectionString: postgres.dsn });
    const client = await admin.connect();
    try {
      await client.query("BEGIN");
      await client.query(
        "LOCK TABLE workflow_instance IN ACCESS EXCLUSIVE MODE",
      );
      await expect(
        new ProcessInstanceQueries(database).detail("missing"),
      ).rejects.toMatchObject({ code: "57014" });
    } finally {
      await client.query("ROLLBACK");
      client.release();
      await admin.end();
    }
    await database.assertReady();
    await expect(
      new ProcessInstanceQueries(database).detail("missing"),
    ).resolves.toBeNull();
  });

  it.each(["0", "-1", "NaN", "1.5"])(
    "rejects invalid resource limit %s",
    (value) => {
      process.env.MONITOR_POSTGRES_POOL_MAX = value;
      expect(() => new MonitorDatabase(postgres.readOnlyDsn)).toThrow(
        "MONITOR_POSTGRES_POOL_MAX",
      );
    },
  );
});
