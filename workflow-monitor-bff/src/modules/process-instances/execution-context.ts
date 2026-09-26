import { ApiProperty } from "@nestjs/swagger";
import type { PoolClient } from "pg";

export class ContextJob {
  @ApiProperty() id!: string;
  @ApiProperty() type!: string;
  @ApiProperty({ enum: ["UNLOCKED", "LOCKED"] }) status!: string;
  @ApiProperty({ nullable: true, type: String }) workerId!: string | null;
  @ApiProperty({ nullable: true, type: String }) lockedAt!: string | null;
  @ApiProperty({ nullable: true, type: String }) lockExpiresAt!: string | null;
  @ApiProperty() createdAt!: string;
  @ApiProperty() retriesRemaining!: number;
}

export class ContextTask {
  @ApiProperty() id!: string;
  @ApiProperty({ nullable: true, type: String }) assignee!: string | null;
  @ApiProperty({ nullable: true, type: String }) assigneeGroup!: string | null;
}

export class ContextTimer {
  @ApiProperty() id!: string;
  @ApiProperty() fireAt!: string;
  @ApiProperty() targetStepId!: string;
  @ApiProperty() interrupting!: boolean;
}

export class ExecutionContext {
  @ApiProperty() stepId!: string;
  @ApiProperty({ nullable: true, type: String }) executionId!: string | null;
  @ApiProperty({ nullable: true, type: String }) stepType!: string | null;
  @ApiProperty({ nullable: true, type: String }) status!: string | null;
  @ApiProperty({ nullable: true, type: Date }) startedAt!: Date | null;
  @ApiProperty({
    enum: ["signal", "userTask", "jobAvailable", "jobLocked", "unavailable"],
  })
  reason!: "signal" | "userTask" | "jobAvailable" | "jobLocked" | "unavailable";
  @ApiProperty({ nullable: true, type: String }) unavailableReason!:
    | string
    | null;
  @ApiProperty({ nullable: true, type: ContextJob }) job!: ContextJob | null;
  @ApiProperty({ nullable: true, type: ContextTask }) task!: ContextTask | null;
  @ApiProperty({ type: [ContextTimer] }) timers!: ContextTimer[];
  @ApiProperty() timersTruncated!: boolean;
}

interface ContextRow {
  step_id: string;
  execution_id: string | null;
  step_type: string | null;
  status: string | null;
  started_at: Date | null;
  running_count: string;
  job: ContextJob | null;
  job_count: string | null;
  task: ContextTask | null;
  task_count: string | null;
  timers: ContextTimer[];
}

// Return only evidence for current executions; retired deliveries and obsolete
// schedules must never masquerade as something the instance is still waiting on.
export const EXECUTION_CONTEXT_SQL = `
  WITH live_jobs AS MATERIALIZED (
    SELECT id, step_execution_id, job_type, status, worker_id, locked_at,
      lock_expires_at, created_at, retries_remaining
    FROM job WHERE instance_id = $1 AND status IN ('UNLOCKED', 'LOCKED')
  ), pending_timers AS MATERIALIZED (
    SELECT id, step_execution_id, fire_at, target_step_id, interrupting
    FROM boundary_event_schedule WHERE instance_id = $1 AND fired = false
  )
  SELECT current.step_id, execution.id AS execution_id,
    execution.step_type, execution.status, execution.started_at,
    execution.running_count, live_job.job, live_job.job_count,
    live_task.task, live_task.task_count,
    COALESCE(timers.items, '[]'::jsonb) AS timers
  FROM unnest($2::text[]) WITH ORDINALITY AS current(step_id, position)
  LEFT JOIN LATERAL (
    SELECT id, step_type, status, started_at,
      count(*) FILTER (WHERE status = 'RUNNING') OVER () AS running_count
    FROM step_execution WHERE instance_id = $1 AND step_id = current.step_id
    ORDER BY attempt_number DESC, started_at DESC, id DESC LIMIT 1
  ) execution ON true
  LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', id, 'type', job_type, 'status', status,
      'workerId', worker_id, 'lockedAt', locked_at, 'lockExpiresAt', lock_expires_at,
      'createdAt', created_at, 'retriesRemaining', retries_remaining) AS job,
      count(*) OVER () AS job_count
    FROM live_jobs WHERE step_execution_id = execution.id
      AND execution.status = 'RUNNING' AND execution.step_type = 'SERVICE_TASK'
    ORDER BY created_at DESC, id DESC LIMIT 1
  ) live_job ON true
  LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', id, 'assignee', assignee,
      'assigneeGroup', assignee_group) AS task, count(*) OVER () AS task_count
    FROM user_task WHERE step_execution_id = execution.id AND instance_id = $1
      AND status = 'OPEN' AND execution.status = 'RUNNING' AND execution.step_type = 'USER_TASK'
    ORDER BY created_at DESC, id DESC LIMIT 1
  ) live_task ON true
  LEFT JOIN LATERAL (
    SELECT jsonb_agg(item ORDER BY fire_at, id) AS items FROM (
      SELECT id, fire_at, jsonb_build_object('id', id, 'fireAt', fire_at,
        'targetStepId', target_step_id, 'interrupting', interrupting) AS item
      FROM pending_timers
      WHERE step_execution_id = execution.id AND execution.status = 'RUNNING'
      ORDER BY fire_at, id LIMIT 101
    ) pending
  ) timers ON true
  ORDER BY current.position
`;

export async function readExecutionContext(
  client: Pick<PoolClient, "query">,
  instanceId: string,
  stepIds: string[],
): Promise<ExecutionContext[]> {
  if (stepIds.length === 0) return [];
  const result = await client.query<ContextRow>(EXECUTION_CONTEXT_SQL, [
    instanceId,
    stepIds,
  ]);
  return result.rows.map((row) => {
    let reason: ExecutionContext["reason"] = "unavailable";
    let unavailableReason: string | null = "No running execution evidence";
    if (
      Number(row.running_count) > 1 ||
      Number(row.job_count) > 1 ||
      Number(row.task_count) > 1
    ) {
      unavailableReason = "Conflicting live execution records";
    } else if (row.status === "RUNNING") {
      if (row.step_type === "WAIT") reason = "signal";
      if (row.step_type === "USER_TASK" && row.task) reason = "userTask";
      if (row.step_type === "SERVICE_TASK" && row.job) {
        reason = row.job.status === "LOCKED" ? "jobLocked" : "jobAvailable";
      }
      unavailableReason =
        reason === "unavailable"
          ? "Related evidence is missing or this step type is unsupported"
          : null;
    }
    const valid = reason !== "unavailable";
    return {
      stepId: row.step_id,
      executionId: row.execution_id,
      stepType: row.step_type,
      status: row.status,
      startedAt: row.started_at,
      reason,
      unavailableReason,
      job: valid && row.step_type === "SERVICE_TASK" ? row.job : null,
      task: valid && row.step_type === "USER_TASK" ? row.task : null,
      timers: row.timers.slice(0, 100),
      timersTruncated: row.timers.length > 100,
    };
  });
}
