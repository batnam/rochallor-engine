import { createHash } from "node:crypto";

import { BadRequestException, Inject, Injectable } from "@nestjs/common";
import type { PoolClient } from "pg";

import { MonitorDatabase } from "../../common/database/monitor-database";
import { isUtcTimestamp } from "../../common/utils/utc-timestamp";
import type {
  ProcessInstanceCursor,
  ProcessInstanceDetail,
  ProcessInstanceFilters,
  ProcessInstanceListItem,
  ProcessInstanceQuery,
  ProcessInstanceRow,
  StepExecutionCursor,
  StepExecutionListItem,
  StepExecutionQuery,
  WorkflowDefinitionDocument,
} from "./dto/process-instance.dto";

const PROCESS_INSTANCE_STATUSES = new Set([
  "ACTIVE",
  "WAITING",
  "COMPLETED",
  "FAILED",
  "CANCELLED",
]);

function statusValues(status: string | string[] | undefined): string[] {
  if (status === undefined) {
    return [];
  }
  return Array.isArray(status) ? status : [status];
}

function validateFilters(filters: ProcessInstanceFilters): void {
  const statuses = statusValues(filters.status);
  if (statuses.some((status) => !PROCESS_INSTANCE_STATUSES.has(status))) {
    throw new BadRequestException("Unknown Process Instance status");
  }
  for (const timestamp of [filters.from, filters.to]) {
    if (timestamp && !isUtcTimestamp(timestamp)) {
      throw new BadRequestException("Invalid UTC timestamp");
    }
  }
  if (
    filters.from &&
    filters.to &&
    Date.parse(filters.from) >= Date.parse(filters.to)
  ) {
    throw new BadRequestException("Start-time range must increase");
  }
}

function filterFingerprint(filters: ProcessInstanceFilters): string {
  const statuses = [...statusValues(filters.status)].sort();
  return createHash("sha256")
    .update(
      JSON.stringify({
        definitionId: filters.definitionId ?? null,
        statuses,
        businessKey: filters.businessKey ?? null,
        from: filters.from ?? null,
        to: filters.to ?? null,
      }),
    )
    .digest("base64url");
}

function encodeCursor(
  item: ProcessInstanceListItem,
  filters: ProcessInstanceFilters,
): string {
  const cursor: ProcessInstanceCursor = {
    v: 1,
    startedAt: item.startedAt.toISOString(),
    id: item.id,
    filters: filterFingerprint(filters),
  };
  return Buffer.from(JSON.stringify(cursor)).toString("base64url");
}

function decodeCursor(
  value: string,
  filters: ProcessInstanceFilters,
): ProcessInstanceCursor {
  try {
    if (!/^[A-Za-z0-9_-]+$/.test(value)) {
      throw new Error("Invalid cursor encoding");
    }
    const decoded = Buffer.from(value, "base64url");
    if (decoded.toString("base64url") !== value) {
      throw new Error("Non-canonical cursor encoding");
    }
    const cursor = JSON.parse(
      decoded.toString("utf8"),
    ) as Partial<ProcessInstanceCursor>;
    if (
      cursor.v !== 1 ||
      typeof cursor.startedAt !== "string" ||
      Number.isNaN(Date.parse(cursor.startedAt)) ||
      typeof cursor.id !== "string" ||
      cursor.id.length === 0 ||
      cursor.filters !== filterFingerprint(filters)
    ) {
      throw new Error("Invalid cursor");
    }
    return cursor as ProcessInstanceCursor;
  } catch {
    throw new BadRequestException("Invalid cursor");
  }
}

function encodeStepExecutionCursor(
  item: StepExecutionListItem,
  instanceId: string,
): string {
  const cursor: StepExecutionCursor = {
    v: 1,
    startedAt: item.startedAt.toISOString(),
    id: item.id,
    instanceId,
  };
  return Buffer.from(JSON.stringify(cursor)).toString("base64url");
}

function decodeStepExecutionCursor(
  value: string,
  instanceId: string,
): StepExecutionCursor {
  try {
    if (!/^[A-Za-z0-9_-]+$/.test(value)) {
      throw new Error("Invalid cursor encoding");
    }
    const decoded = Buffer.from(value, "base64url");
    if (decoded.toString("base64url") !== value) {
      throw new Error("Non-canonical cursor encoding");
    }
    const cursor = JSON.parse(
      decoded.toString("utf8"),
    ) as Partial<StepExecutionCursor>;
    if (
      cursor.v !== 1 ||
      typeof cursor.startedAt !== "string" ||
      !isUtcTimestamp(cursor.startedAt) ||
      typeof cursor.id !== "string" ||
      cursor.id.length === 0 ||
      cursor.instanceId !== instanceId
    ) {
      throw new Error("Invalid cursor");
    }
    return cursor as StepExecutionCursor;
  } catch {
    throw new BadRequestException("Invalid cursor");
  }
}

function pageSize(value: string | undefined): number {
  const parsed = value === undefined ? 50 : Number(value);
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 100) {
    throw new BadRequestException("Page size must be between 1 and 100");
  }
  return parsed;
}

@Injectable()
export class ProcessInstanceQueries {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
  ) {}

  async list(query: ProcessInstanceQuery): Promise<{
    items: ProcessInstanceListItem[];
    nextCursor: string | null;
  }> {
    const filters: ProcessInstanceFilters = {
      definitionId: query.definitionId,
      status: query.status,
      businessKey: query.businessKey,
      from: query.from,
      to: query.to,
    };
    validateFilters(filters);
    const limit = pageSize(query.pageSize);
    const cursor = query.cursor
      ? decodeCursor(query.cursor, filters)
      : undefined;
    const conditions: string[] = [];
    const values: unknown[] = [];
    const addCondition = (condition: string, value: unknown): void => {
      values.push(value);
      conditions.push(condition.replace("?", `$${values.length}`));
    };

    if (filters.definitionId) {
      addCondition("definition_id = ?", filters.definitionId);
    }
    const statuses = statusValues(filters.status);
    if (statuses.length > 0) {
      addCondition("status = ANY(?::text[])", statuses);
    }
    if (filters.businessKey) {
      addCondition("business_key = ?", filters.businessKey);
    }
    if (filters.from) {
      addCondition("started_at >= ?", filters.from);
    }
    if (filters.to) {
      addCondition("started_at < ?", filters.to);
    }
    if (cursor) {
      addCondition("(started_at, id) < (?, ?)", cursor.startedAt);
      values.push(cursor.id);
      conditions[conditions.length - 1] = conditions[
        conditions.length - 1
      ].replace("?)", `$${values.length})`);
    }

    const where =
      conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";
    values.push(limit + 1);
    const result = await this.database.query<ProcessInstanceRow>(
      `
      SELECT
        id,
        definition_id,
        definition_version,
        status,
        current_step_ids,
        started_at,
        completed_at,
        failure_reason,
        business_key
      FROM workflow_instance
      ${where}
      ORDER BY started_at DESC, id DESC
      LIMIT $${values.length}
    `,
      values,
    );

    const mapped = result.rows.map((row) => ({
      id: row.id,
      definitionId: row.definition_id,
      definitionVersion: row.definition_version,
      status: row.status,
      currentStepIds: row.current_step_ids,
      startedAt: row.started_at,
      completedAt: row.completed_at,
      failureReason: row.failure_reason,
      businessKey: row.business_key,
    }));
    const items = mapped.slice(0, limit);
    return {
      items,
      nextCursor:
        mapped.length > limit
          ? encodeCursor(items[items.length - 1], filters)
          : null,
    };
  }

  async detail(instanceId: string): Promise<ProcessInstanceDetail | null> {
    const client = await this.database.connect();
    try {
      await client.query(
        "BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY",
      );
      const instanceResult = await client.query<ProcessInstanceRow>(
        `
          SELECT
            id,
            definition_id,
            definition_version,
            status,
            current_step_ids,
            started_at,
            completed_at,
            failure_reason,
            business_key
          FROM workflow_instance
          WHERE id = $1
        `,
        [instanceId],
      );
      const instanceRow = instanceResult.rows[0];
      if (!instanceRow) {
        await client.query("ROLLBACK");
        return null;
      }
      const definitionResult = await client.query<{
        raw_json: Omit<WorkflowDefinitionDocument, "version">;
        version: number;
      }>(
        `
          SELECT raw_json, version
          FROM workflow_definition
          WHERE id = $1 AND version = $2
        `,
        [instanceRow.definition_id, instanceRow.definition_version],
      );
      const definitionRow = definitionResult.rows[0];
      if (!definitionRow) {
        await client.query("ROLLBACK");
        return null;
      }
      const executionResult = await client.query<{
        id: string;
        step_id: string;
        status: string;
        attempt_number: number;
      }>(
        `
          SELECT DISTINCT ON (step_id)
            id,
            step_id,
            status,
            attempt_number
          FROM step_execution
          WHERE instance_id = $1
          ORDER BY
            step_id,
            attempt_number DESC,
            started_at DESC,
            id DESC
        `,
        [instanceId],
      );
      let failedStepId: string | null = null;
      if (instanceRow.status === "FAILED") {
        const failedExecutionResult = await client.query<{ step_id: string }>(
          `
            SELECT step_id
            FROM step_execution
            WHERE instance_id = $1 AND status = 'FAILED'
            ORDER BY ended_at DESC NULLS LAST, started_at DESC, id DESC
            LIMIT 1
          `,
          [instanceId],
        );
        failedStepId = failedExecutionResult.rows[0]?.step_id ?? null;
      }
      await client.query("COMMIT");

      const instance: ProcessInstanceListItem = {
        id: instanceRow.id,
        definitionId: instanceRow.definition_id,
        definitionVersion: instanceRow.definition_version,
        status: instanceRow.status,
        currentStepIds: instanceRow.current_step_ids,
        startedAt: instanceRow.started_at,
        completedAt: instanceRow.completed_at,
        failureReason: instanceRow.failure_reason,
        businessKey: instanceRow.business_key,
      };
      return {
        instance,
        definition: {
          ...definitionRow.raw_json,
          version: definitionRow.version,
        } as WorkflowDefinitionDocument,
        executionOverlay: {
          currentTokenStepIds:
            instance.status === "ACTIVE" || instance.status === "WAITING"
              ? instance.currentStepIds
              : [],
          failedStepId,
          latestByStep: executionResult.rows.map((execution) => ({
            executionId: execution.id,
            stepId: execution.step_id,
            status: execution.status,
            attemptNumber: execution.attempt_number,
          })),
        },
      };
    } catch (error) {
      await rollback(client);
      throw error;
    } finally {
      client.release();
    }
  }

  async listStepExecutions(
    instanceId: string,
    query: StepExecutionQuery,
  ): Promise<{
    items: StepExecutionListItem[];
    nextCursor: string | null;
  }> {
    const limit = pageSize(query.pageSize);
    const cursor = query.cursor
      ? decodeStepExecutionCursor(query.cursor, instanceId)
      : undefined;
    const values: unknown[] = [instanceId];
    let cursorCondition = "";
    if (cursor) {
      values.push(cursor.startedAt, cursor.id);
      cursorCondition = "AND (started_at, id) < ($2, $3)";
    }
    values.push(limit + 1);
    const result = await this.database.query<{
      id: string;
      step_id: string;
      step_type: string;
      attempt_number: number;
      status: string;
      started_at: Date;
      ended_at: Date | null;
      has_failure: boolean;
      has_input_snapshot: boolean;
      has_output_snapshot: boolean;
    }>(
      `
        SELECT
          id,
          step_id,
          step_type,
          attempt_number,
          status,
          started_at,
          ended_at,
          failure_reason IS NOT NULL AS has_failure,
          input_snapshot IS NOT NULL AS has_input_snapshot,
          output_snapshot IS NOT NULL AS has_output_snapshot
        FROM step_execution
        WHERE instance_id = $1
        ${cursorCondition}
        ORDER BY started_at DESC, id DESC
        LIMIT $${values.length}
      `,
      values,
    );
    const mapped = result.rows.map((row) => ({
      id: row.id,
      stepId: row.step_id,
      stepType: row.step_type,
      attemptNumber: row.attempt_number,
      status: row.status,
      startedAt: row.started_at,
      endedAt: row.ended_at,
      hasFailure: row.has_failure,
      hasInputSnapshot: row.has_input_snapshot,
      hasOutputSnapshot: row.has_output_snapshot,
    }));
    const items = mapped.slice(0, limit);
    return {
      items,
      nextCursor:
        mapped.length > limit
          ? encodeStepExecutionCursor(items[items.length - 1], instanceId)
          : null,
    };
  }
}

async function rollback(client: PoolClient): Promise<void> {
  try {
    await client.query("ROLLBACK");
  } catch {
    // Preserve the original database error.
  }
}
