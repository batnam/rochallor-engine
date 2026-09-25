import { BadRequestException, Inject, Injectable } from "@nestjs/common";
import { MonitorDatabase } from "../../common/database/monitor-database";

export interface OverviewQuery {
  cursor?: string;
  pageSize?: string;
  workflow?: string;
  definitionId?: string;
  definitionVersion?: string;
  minStepAgeSeconds?: string;
}
export interface DefinitionCount {
  definitionId: string;
  definitionVersion: number;
  name: string;
  active: number;
  waiting: number;
  failed: number;
}
export interface StepCount {
  stepId: string;
  instances: number;
  aged: number;
  ageUnavailable: number;
}

function positiveInteger(
  value: string | undefined,
  fallback: number,
  maximum: number,
  name: string,
): number {
  const number = value === undefined ? fallback : Number(value);
  if (
    (value !== undefined &&
      (typeof value !== "string" || !/^\d+$/.test(value))) ||
    !Number.isSafeInteger(number) ||
    number < 1 ||
    number > maximum
  ) {
    throw new BadRequestException(
      `${name} must be an integer between 1 and ${maximum}`,
    );
  }
  return number;
}

function decodeCursor(
  value: string | undefined,
  scope: string,
): [string, number] {
  if (value === undefined) return ["", 0];
  try {
    if (typeof value !== "string" || !/^[\w-]+$/.test(value)) throw new Error();
    const bytes = Buffer.from(value, "base64url");
    const decoded = JSON.parse(bytes.toString("utf8"));
    if (
      bytes.toString("base64url") !== value ||
      decoded.scope !== scope ||
      decoded.v !== 1 ||
      typeof decoded.id !== "string" ||
      !decoded.id ||
      !Number.isSafeInteger(decoded.version) ||
      decoded.version < 0 ||
      decoded.version > 2147483647
    )
      throw new Error();
    return [decoded.id, decoded.version];
  } catch {
    throw new BadRequestException("Invalid Overview cursor");
  }
}
function encodeCursor(scope: string, id: string, version = 0): string {
  return Buffer.from(JSON.stringify({ v: 1, scope, id, version })).toString(
    "base64url",
  );
}

@Injectable()
export class OverviewQueries {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
  ) {}

  async definitions(query: OverviewQuery) {
    const limit = positiveInteger(query.pageSize, 50, 100, "Page size");
    if (query.workflow !== undefined && typeof query.workflow !== "string") {
      throw new BadRequestException("Workflow search must be a string");
    }
    const workflow = query.workflow?.trim() ?? "";
    const scope = workflow
      ? JSON.stringify(["definitions", workflow])
      : "definitions";
    const [afterId, afterVersion] = decodeCursor(query.cursor, scope);
    const result = await this.database.query<{
      observed_at: Date;
      items: DefinitionCount[];
    }>(
      `
      WITH counts AS (
        SELECT i.definition_id AS "definitionId", i.definition_version AS "definitionVersion",
          count(*) FILTER (WHERE i.status = 'ACTIVE') AS active,
          count(*) FILTER (WHERE i.status = 'WAITING') AS waiting,
          count(*) FILTER (WHERE i.status = 'FAILED') AS failed
        FROM workflow_instance i
        WHERE ($1::text = '' OR (i.definition_id, i.definition_version) > ($1, $2::int))
        GROUP BY i.definition_id, i.definition_version
      ), page AS (
        SELECT counts.*, COALESCE(d.name, counts."definitionId") AS name
        FROM counts LEFT JOIN workflow_definition d
          ON d.id = counts."definitionId" AND d.version = counts."definitionVersion"
        WHERE ($4::text = '' OR strpos(lower(counts."definitionId"), lower($4)) > 0
          OR strpos(lower(d.name), lower($4)) > 0)
        ORDER BY counts."definitionId", counts."definitionVersion" LIMIT $3
      )
      SELECT transaction_timestamp() AS observed_at,
        COALESCE(jsonb_agg(page ORDER BY "definitionId", "definitionVersion"), '[]'::jsonb) AS items FROM page
    `,
      [afterId, afterVersion, limit + 1, workflow],
    );
    const { observed_at: observedAt, items: rows } = result.rows[0];
    const items = rows.slice(0, limit);
    const last = items[items.length - 1];
    return {
      observedAt,
      scope: "All retained instances grouped by definition and version",
      items,
      nextCursor:
        rows.length > limit
          ? encodeCursor(scope, last.definitionId, last.definitionVersion)
          : null,
    };
  }

  async steps(query: OverviewQuery) {
    if (
      typeof query.definitionId !== "string" ||
      !query.definitionId ||
      query.definitionVersion === undefined
    ) {
      throw new BadRequestException("Definition ID and version are required");
    }
    const version = positiveInteger(
      query.definitionVersion,
      1,
      2147483647,
      "Definition version",
    );
    const age = positiveInteger(
      query.minStepAgeSeconds,
      1800,
      2147483647,
      "Minimum step age",
    );
    const limit = positiveInteger(query.pageSize, 50, 100, "Page size");
    const scope = JSON.stringify([query.definitionId, version, age]);
    const [afterStep] = decodeCursor(query.cursor, scope);
    const result = await this.database.query<{
      observed_at: Date;
      cutoff: Date;
      items: StepCount[];
    }>(
      `
      WITH page AS (
        SELECT current.step_id AS "stepId", count(DISTINCT i.id) AS instances,
          count(DISTINCT i.id) FILTER (WHERE execution.status = 'RUNNING'
            AND execution.started_at < transaction_timestamp() - ($3::int * interval '1 second')) AS aged,
          count(DISTINCT i.id) FILTER (WHERE execution.status IS DISTINCT FROM 'RUNNING') AS "ageUnavailable"
        FROM workflow_instance i CROSS JOIN LATERAL unnest(i.current_step_ids) AS current(step_id)
        LEFT JOIN LATERAL (
          SELECT status, started_at FROM step_execution WHERE instance_id = i.id AND step_id = current.step_id
          ORDER BY attempt_number DESC, started_at DESC, id DESC LIMIT 1
        ) execution ON true
        WHERE i.definition_id = $1 AND i.definition_version = $2
          AND i.status IN ('ACTIVE', 'WAITING') AND ($4::text = '' OR current.step_id > $4)
        GROUP BY current.step_id ORDER BY current.step_id LIMIT $5
      )
      SELECT transaction_timestamp() AS observed_at,
        transaction_timestamp() - ($3::int * interval '1 second') AS cutoff,
        COALESCE(jsonb_agg(page ORDER BY "stepId"), '[]'::jsonb) AS items FROM page
    `,
      [query.definitionId, version, age, afterStep, limit + 1],
    );
    const {
      observed_at: observedAt,
      cutoff: stepStartedBefore,
      items: rows,
    } = result.rows[0];
    const items = rows.slice(0, limit);
    return {
      observedAt,
      stepStartedBefore,
      definitionId: query.definitionId,
      definitionVersion: version,
      minStepAgeSeconds: age,
      scope:
        "Distinct ACTIVE/WAITING instances per current step; step counts can overlap",
      items,
      nextCursor:
        rows.length > limit
          ? encodeCursor(scope, items[items.length - 1].stepId)
          : null,
    };
  }
}
