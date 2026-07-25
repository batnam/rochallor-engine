import { createHash } from "node:crypto";

import { BadRequestException, Inject, Injectable } from "@nestjs/common";

import { MonitorDatabase } from "../../common/database/monitor-database";
import { isUtcTimestamp } from "../../common/utils/utc-timestamp";
import type {
  IncidentCursor,
  IncidentDetail,
  IncidentDetailRow,
  IncidentFilters,
  IncidentListItem,
  IncidentQuery,
  IncidentRow,
} from "./dto/incident.dto";

function validateFilters(filters: IncidentFilters): void {
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
    throw new BadRequestException("Occurrence-time range must increase");
  }
}

function filterFingerprint(filters: IncidentFilters): string {
  return createHash("sha256")
    .update(
      JSON.stringify({
        definitionId: filters.definitionId ?? null,
        jobType: filters.jobType ?? null,
        from: filters.from ?? null,
        to: filters.to ?? null,
      }),
    )
    .digest("base64url");
}

function encodeCursor(
  item: IncidentListItem,
  filters: IncidentFilters,
): string {
  const cursor: IncidentCursor = {
    v: 1,
    occurredAt: item.occurredAt.toISOString(),
    id: item.id,
    filters: filterFingerprint(filters),
  };
  return Buffer.from(JSON.stringify(cursor)).toString("base64url");
}

function decodeCursor(value: string, filters: IncidentFilters): IncidentCursor {
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
    ) as Partial<IncidentCursor>;
    if (
      cursor.v !== 1 ||
      typeof cursor.occurredAt !== "string" ||
      !isUtcTimestamp(cursor.occurredAt) ||
      typeof cursor.id !== "string" ||
      cursor.id.length === 0 ||
      cursor.filters !== filterFingerprint(filters)
    ) {
      throw new Error("Invalid cursor");
    }
    return cursor as IncidentCursor;
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
export class IncidentQueries {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
  ) {}

  async list(query: IncidentQuery): Promise<{
    items: IncidentListItem[];
    nextCursor: string | null;
  }> {
    const filters: IncidentFilters = {
      definitionId: query.definitionId,
      jobType: query.jobType,
      from: query.from,
      to: query.to,
    };
    validateFilters(filters);
    const limit = pageSize(query.pageSize);
    const cursor = query.cursor
      ? decodeCursor(query.cursor, filters)
      : undefined;
    const conditions = [
      "execution.status = 'FAILED'",
      "instance.status <> 'CANCELLED'",
    ];
    const values: unknown[] = [];
    const addCondition = (condition: string, value: unknown): void => {
      values.push(value);
      conditions.push(condition.replace("?", `$${values.length}`));
    };
    if (filters.definitionId) {
      addCondition("instance.definition_id = ?", filters.definitionId);
    }
    if (filters.jobType) {
      addCondition("related_job.job_type = ?", filters.jobType);
    }
    if (filters.from) {
      addCondition("execution.ended_at >= ?", filters.from);
    }
    if (filters.to) {
      addCondition("execution.ended_at < ?", filters.to);
    }
    if (cursor) {
      addCondition(
        "(execution.ended_at, execution.id) < (?, ?)",
        cursor.occurredAt,
      );
      values.push(cursor.id);
      conditions[conditions.length - 1] = conditions[
        conditions.length - 1
      ].replace("?)", `$${values.length})`);
    }
    values.push(limit + 1);

    const result = await this.database.query<IncidentRow>(
      `
      SELECT
        execution.id,
        execution.instance_id,
        instance.definition_id,
        instance.definition_version,
        definition.name AS definition_name,
        execution.step_id,
        execution.step_type,
        execution.attempt_number,
        execution.ended_at AS occurred_at,
        related_job.id AS job_id,
        related_job.job_type,
        related_job.status AS job_status
      FROM step_execution AS execution
      JOIN workflow_instance AS instance
        ON instance.id = execution.instance_id
      JOIN workflow_definition AS definition
        ON definition.id = instance.definition_id
       AND definition.version = instance.definition_version
      LEFT JOIN (
        SELECT DISTINCT ON (step_execution_id)
          id,
          step_execution_id,
          job_type,
          status
        FROM job
        ORDER BY step_execution_id, created_at DESC, id DESC
      ) AS related_job
        ON related_job.step_execution_id = execution.id
      WHERE ${conditions.join(" AND ")}
      ORDER BY execution.ended_at DESC, execution.id DESC
      LIMIT $${values.length}
    `,
      values,
    );
    const mapped = result.rows.map(mapListItem);
    const items = mapped.slice(0, limit);
    return {
      items,
      nextCursor:
        mapped.length > limit
          ? encodeCursor(items[items.length - 1], filters)
          : null,
    };
  }

  async detail(incidentId: string): Promise<IncidentDetail | null> {
    const result = await this.database.query<IncidentDetailRow>(
      `
        SELECT
          execution.id,
          execution.instance_id,
          instance.definition_id,
          instance.definition_version,
          definition.name AS definition_name,
          execution.step_id,
          execution.step_type,
          execution.attempt_number,
          execution.ended_at AS occurred_at,
          execution.failure_reason AS error_details,
          instance.status AS instance_status,
          instance.business_key,
          related_job.id AS job_id,
          related_job.job_type,
          related_job.status AS job_status
        FROM step_execution AS execution
        JOIN workflow_instance AS instance
          ON instance.id = execution.instance_id
        JOIN workflow_definition AS definition
          ON definition.id = instance.definition_id
         AND definition.version = instance.definition_version
        LEFT JOIN (
          SELECT DISTINCT ON (step_execution_id)
            id,
            step_execution_id,
            job_type,
            status
          FROM job
          ORDER BY step_execution_id, created_at DESC, id DESC
        ) AS related_job
          ON related_job.step_execution_id = execution.id
        WHERE execution.id = $1
          AND execution.status = 'FAILED'
          AND instance.status <> 'CANCELLED'
      `,
      [incidentId],
    );
    const row = result.rows[0];
    if (!row) {
      return null;
    }
    return {
      ...mapListItem(row),
      errorDetails: row.error_details,
      processInstance: {
        id: row.instance_id,
        status: row.instance_status,
        businessKey: row.business_key,
      },
    };
  }
}

function mapListItem(row: IncidentRow): IncidentListItem {
  return {
    id: row.id,
    processInstanceId: row.instance_id,
    definitionId: row.definition_id,
    definitionVersion: row.definition_version,
    definitionName: row.definition_name,
    stepId: row.step_id,
    stepType: row.step_type,
    attemptNumber: row.attempt_number,
    occurredAt: row.occurred_at,
    job:
      row.job_id && row.job_type && row.job_status
        ? {
            id: row.job_id,
            type: row.job_type,
            status: row.job_status,
          }
        : null,
  };
}
