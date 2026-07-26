import type { OnApplicationShutdown } from "@nestjs/common";
import {
  Pool,
  type PoolClient,
  type QueryResult,
  type QueryResultRow,
} from "pg";

export class MonitorDatabase implements OnApplicationShutdown {
  private readonly pool: Pool | undefined;

  constructor(postgresDsn: string | undefined) {
    this.pool = postgresDsn
      ? new Pool({ connectionString: postgresDsn })
      : undefined;
    this.pool?.on("error", () => {
      // Request and readiness paths report database availability.
    });
  }

  query<Row extends QueryResultRow>(
    text: string,
    values?: unknown[],
  ): Promise<QueryResult<Row>> {
    return this.getPool().query<Row>(text, values);
  }

  connect(): Promise<PoolClient> {
    return this.getPool().connect();
  }

  async assertReady(): Promise<void> {
    await this.query("SELECT status FROM workflow_instance LIMIT 0");
  }

  async onApplicationShutdown(): Promise<void> {
    await this.pool?.end();
  }

  private getPool(): Pool {
    if (!this.pool) {
      throw new Error("PostgreSQL is not configured");
    }
    return this.pool;
  }
}
