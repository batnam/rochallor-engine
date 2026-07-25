import { execFileSync } from "node:child_process";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  PostgreSqlContainer,
  type StartedPostgreSqlContainer,
} from "@testcontainers/postgresql";
import { Pool } from "pg";

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const composeFiles = [
  path.join(repositoryRoot, "deploy/docker-compose.monitor.quickstart.yml"),
  path.join(repositoryRoot, "e2e/docker-compose-monitor.yml"),
];
const composeProject = "rochallor-monitor-e2e";

export default async function globalSetup(): Promise<() => Promise<void>> {
  const postgres = await startReleasePostgres();
  const adminDsn = postgres.getConnectionUri();
  await seedReleaseFixture(adminDsn);

  const containerDsn = new URL(adminDsn);
  containerDsn.username = "rochallor_monitor_e2e";
  containerDsn.password = "monitor_e2e_password";
  containerDsn.hostname = "host.docker.internal";
  const composeEnvironment = {
    ...process.env,
    MONITOR_PORT: "13001",
    MONITOR_POSTGRES_DSN: containerDsn.toString(),
  };
  const composeArguments = [
    "compose",
    "--project-name",
    composeProject,
    ...composeFiles.flatMap((composeFile) => ["-f", composeFile]),
  ];

  try {
    execFileSync("docker", [...composeArguments, "up", "--build", "--detach"], {
      env: composeEnvironment,
      stdio: "inherit",
    });
    await waitForMonitor();
    process.env.MONITOR_E2E_POSTGRES_CONTAINER_ID = postgres.getId();
  } catch (error) {
    printComposeLogs(composeArguments, composeEnvironment);
    await stopDeployment(composeArguments, composeEnvironment, postgres);
    throw error;
  }

  return () => stopDeployment(composeArguments, composeEnvironment, postgres);
}

async function startReleasePostgres(): Promise<StartedPostgreSqlContainer> {
  const postgres = await new PostgreSqlContainer("postgres:16-alpine").start();
  const pool = new Pool({ connectionString: postgres.getConnectionUri() });
  const migrationsDirectory = path.join(
    repositoryRoot,
    "workflow-engine/migrations",
  );
  const migrationFiles = (await readdir(migrationsDirectory))
    .filter((file) => file.endsWith(".up.sql"))
    .sort();
  try {
    for (const migrationFile of migrationFiles) {
      await pool.query(
        await readFile(path.join(migrationsDirectory, migrationFile), "utf8"),
      );
    }
    await pool.query(`
      CREATE ROLE rochallor_monitor_e2e
      LOGIN
      PASSWORD 'monitor_e2e_password';
      GRANT USAGE ON SCHEMA public TO rochallor_monitor_e2e;
      GRANT SELECT ON ALL TABLES IN SCHEMA public TO rochallor_monitor_e2e;
    `);
  } catch (error) {
    await postgres.stop();
    throw error;
  } finally {
    await pool.end();
  }
  return postgres;
}

async function seedReleaseFixture(adminDsn: string): Promise<void> {
  const pool = new Pool({ connectionString: adminDsn });
  try {
    await pool.query(`
    INSERT INTO workflow_definition (
      id,
      version,
      name,
      raw_json,
      parsed_steps
    ) VALUES (
      'release-flow',
      1,
      'Release Flow',
      '{"id":"release-flow","name":"Release Flow","steps":[{"id":"review","name":"Review","type":"USER_TASK"}]}',
      '[]'
    );

    INSERT INTO workflow_instance (
      id,
      definition_id,
      definition_version,
      status,
      current_step_ids,
      variables,
      started_at,
      business_key
    )
    SELECT
      'release-instance-' || lpad(series::text, 3, '0'),
      'release-flow',
      1,
      'WAITING',
      ARRAY['review'],
      '{"releaseSecret":"never-log-release-secret"}',
      '2026-04-01T00:00:00Z'::timestamptz + series * interval '1 second',
      'release-' || lpad(series::text, 3, '0')
    FROM generate_series(1, 52) AS series;

    INSERT INTO workflow_definition (
      id,
      version,
      name,
      raw_json,
      parsed_steps
    ) VALUES (
      'parallel-release-flow',
      1,
      'Parallel Release Flow',
      '{"id":"parallel-release-flow","name":"Parallel Release Flow","steps":[{"id":"review-a","name":"Review A","type":"USER_TASK"},{"id":"review-b","name":"Review B","type":"USER_TASK"}]}',
      '[]'
    );

    INSERT INTO workflow_instance (
      id,
      definition_id,
      definition_version,
      status,
      current_step_ids,
      variables,
      started_at,
      business_key
    ) VALUES (
      'release-parallel',
      'parallel-release-flow',
      1,
      'ACTIVE',
      ARRAY['review-a', 'review-b'],
      '{}',
      '2026-05-01T00:00:00Z',
      'parallel-release'
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
        'parallel-a-attempt-1',
        'release-parallel',
        'review-a',
        'USER_TASK',
        1,
        'FAILED',
        '2026-05-01T00:00:01Z',
        '2026-05-01T00:00:02Z',
        'first review failed'
      ),
      (
        'parallel-a-attempt-2',
        'release-parallel',
        'review-a',
        'USER_TASK',
        2,
        'RUNNING',
        '2026-05-01T00:00:03Z',
        NULL,
        NULL
      ),
      (
        'parallel-b-attempt-1',
        'release-parallel',
        'review-b',
        'USER_TASK',
        1,
        'RUNNING',
        '2026-05-01T00:00:01Z',
        NULL,
        NULL
      );

    INSERT INTO workflow_instance (
      id,
      definition_id,
      definition_version,
      status,
      current_step_ids,
      variables,
      started_at,
      completed_at,
      business_key
    ) VALUES (
      'release-failed',
      'release-flow',
      1,
      'FAILED',
      '{}',
      '{"cardNumber":"release-sensitive-card"}',
      '2026-05-02T00:00:00Z',
      '2026-05-02T00:00:02Z',
      'failed-release'
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
      failure_reason,
      input_snapshot,
      output_snapshot
    ) VALUES (
      'release-failed-execution',
      'release-failed',
      'review',
      'SERVICE_TASK',
      1,
      'FAILED',
      '2026-05-02T00:00:01Z',
      '2026-05-02T00:00:02Z',
      'release worker rejected the task',
      '{"phase":"before-release"}',
      '{"phase":"after-release"}'
    );

    INSERT INTO job (
      id,
      instance_id,
      step_execution_id,
      job_type,
      status,
      worker_id,
      retries_remaining
    ) VALUES (
      'release-failed-job',
      'release-failed',
      'release-failed-execution',
      'release-worker',
      'FAILED',
      'release-worker-1',
      0
    );
  `);
  } finally {
    await pool.end();
  }
}

async function waitForMonitor(): Promise<void> {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(
        "http://127.0.0.1:13001/api/v1/process-instances",
      );
      if (response.ok) {
        return;
      }
    } catch {
      // The production stack is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw new Error("Rochallor Monitor did not become ready");
}

function printComposeLogs(
  composeArguments: string[],
  environment: NodeJS.ProcessEnv,
): void {
  try {
    execFileSync("docker", [...composeArguments, "logs", "--no-color"], {
      env: environment,
      stdio: "inherit",
    });
  } catch {
    // Preserve the startup failure.
  }
}

async function stopDeployment(
  composeArguments: string[],
  environment: NodeJS.ProcessEnv,
  postgres: StartedPostgreSqlContainer,
): Promise<void> {
  try {
    execFileSync(
      "docker",
      [...composeArguments, "down", "--volumes", "--remove-orphans"],
      {
        env: environment,
        stdio: "ignore",
      },
    );
  } finally {
    await postgres.stop();
  }
}
