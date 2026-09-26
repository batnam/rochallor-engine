import type {
  OnApplicationBootstrap,
  OnApplicationShutdown,
} from "@nestjs/common";
import {
  Pool,
  type PoolClient,
  type QueryResult,
  type QueryResultRow,
  types,
} from "pg";

const { TEXT, INT4, JSONB, TIMESTAMPTZ, BOOL } = types.builtins;
// Only the Engine columns read by Monitor form this contract. Extra columns are allowed.
const REQUIRED_SCHEMA: Record<string, Record<string, number>> = {
  workflow_instance: {
    id: TEXT,
    definition_id: TEXT,
    definition_version: INT4,
    status: TEXT,
    current_step_ids: 1009, // PostgreSQL text[]
    variables: JSONB,
    started_at: TIMESTAMPTZ,
    completed_at: TIMESTAMPTZ,
    failure_reason: TEXT,
    business_key: TEXT,
  },
  workflow_definition: { id: TEXT, version: INT4, name: TEXT, raw_json: JSONB },
  step_execution: {
    id: TEXT,
    instance_id: TEXT,
    step_id: TEXT,
    step_type: TEXT,
    attempt_number: INT4,
    status: TEXT,
    started_at: TIMESTAMPTZ,
    ended_at: TIMESTAMPTZ,
    failure_reason: TEXT,
    input_snapshot: JSONB,
    output_snapshot: JSONB,
  },
  job: {
    id: TEXT,
    instance_id: TEXT,
    worker_id: TEXT,
    locked_at: TIMESTAMPTZ,
    lock_expires_at: TIMESTAMPTZ,
    retries_remaining: INT4,
    step_execution_id: TEXT,
    job_type: TEXT,
    status: TEXT,
    created_at: TIMESTAMPTZ,
  },
  user_task: {
    id: TEXT,
    instance_id: TEXT,
    step_execution_id: TEXT,
    status: TEXT,
    assignee: TEXT,
    assignee_group: TEXT,
    created_at: TIMESTAMPTZ,
  },
  boundary_event_schedule: {
    id: TEXT,
    instance_id: TEXT,
    step_execution_id: TEXT,
    target_step_id: TEXT,
    fire_at: TIMESTAMPTZ,
    interrupting: BOOL,
    fired: BOOL,
  },
};

export class MonitorDatabase
  implements OnApplicationBootstrap, OnApplicationShutdown
{
  private readonly pool: Pool | undefined;

  constructor(postgresDsn: string | undefined) {
    this.pool = postgresDsn
      ? new Pool({
          connectionString: postgresDsn,
          application_name: "rochallor-monitor",
          max: positiveInteger("MONITOR_POSTGRES_POOL_MAX", 5),
          connectionTimeoutMillis: positiveInteger(
            "MONITOR_POSTGRES_CONNECTION_TIMEOUT_MS",
            2_000,
          ),
          statement_timeout: positiveInteger(
            "MONITOR_POSTGRES_STATEMENT_TIMEOUT_MS",
            3_000,
          ),
        })
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
    for (const [table, columns] of Object.entries(REQUIRED_SCHEMA)) {
      try {
        // Identifiers come exclusively from the static contract above, never a request.
        const result = await this.query(
          `SELECT ${Object.keys(columns).join(", ")} FROM ${table} LIMIT 0`,
        );
        for (const field of result.fields) {
          if (field.dataTypeID !== columns[field.name]) {
            throw new Error(
              `column ${field.name} has type OID ${field.dataTypeID}; expected ${columns[field.name]}`,
            );
          }
        }
      } catch (error) {
        throw new Error(
          `Monitor schema check failed for ${table}: ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    }
  }

  async onApplicationBootstrap(): Promise<void> {
    if (this.pool) await this.assertReady();
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

function positiveInteger(name: string, fallback: number): number {
  const value = Number(process.env[name] ?? fallback);
  if (!Number.isSafeInteger(value) || value < 1 || value > 2_147_483_647) {
    throw new Error(
      `${name} must be a positive integer no greater than 2147483647`,
    );
  }
  return value;
}
