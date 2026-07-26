import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

import { PostgreSqlContainer } from "@testcontainers/postgresql";
import { Pool } from "pg";

export interface PostgresFixture {
  dsn: string;
  readOnlyDsn: string;
  query(sql: string): Promise<void>;
  stop(): Promise<void>;
}

export async function startPostgresFixture(): Promise<PostgresFixture> {
  const container = await new PostgreSqlContainer("postgres:16-alpine").start();
  const pool = new Pool({ connectionString: container.getConnectionUri() });
  const migrationsDirectory = path.resolve(
    __dirname,
    "../../../workflow-engine/migrations",
  );
  const migrationFiles = (await readdir(migrationsDirectory))
    .filter((file) => file.endsWith(".up.sql"))
    .sort();

  try {
    for (const migrationFile of migrationFiles) {
      const sql = await readFile(
        path.join(migrationsDirectory, migrationFile),
        "utf8",
      );
      await pool.query(sql);
    }
    await pool.query(`
      CREATE ROLE rochallor_monitor_test
      LOGIN
      PASSWORD 'monitor_test_password'
    `);
    await pool.query("GRANT USAGE ON SCHEMA public TO rochallor_monitor_test");
    await pool.query(
      "GRANT SELECT ON ALL TABLES IN SCHEMA public TO rochallor_monitor_test",
    );
  } catch (error) {
    await pool.end();
    await container.stop();
    throw error;
  }

  await pool.end();
  const readOnlyUrl = new URL(container.getConnectionUri());
  readOnlyUrl.username = "rochallor_monitor_test";
  readOnlyUrl.password = "monitor_test_password";

  return {
    dsn: container.getConnectionUri(),
    readOnlyDsn: readOnlyUrl.toString(),
    query: async (sql: string) => {
      const admin = new Pool({
        connectionString: container.getConnectionUri(),
      });
      try {
        await admin.query(sql);
      } finally {
        await admin.end();
      }
    },
    stop: () => container.stop().then(() => undefined),
  };
}
