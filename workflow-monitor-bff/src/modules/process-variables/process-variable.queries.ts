import { Inject, Injectable } from "@nestjs/common";

import { MonitorDatabase } from "../../common/database/monitor-database";
import type { VariableDocument } from "./dto/variable-document.dto";

export const MAX_JSON_DOCUMENT_BYTES = Symbol("MAX_JSON_DOCUMENT_BYTES");

@Injectable()
export class ProcessVariableQueries {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
    @Inject(MAX_JSON_DOCUMENT_BYTES)
    private readonly maxJsonDocumentBytes: number,
  ) {}

  async current(instanceId: string): Promise<VariableDocument | null> {
    const result = await this.database.query<{ variables: unknown }>(
      "SELECT variables FROM workflow_instance WHERE id = $1",
      [instanceId],
    );
    const row = result.rows[0];
    if (!row) {
      return null;
    }
    return this.variableDocument(row.variables);
  }

  async recorded(
    instanceId: string,
    executionId: string,
  ): Promise<{
    recordedInput: VariableDocument;
    recordedOutput: VariableDocument;
  } | null> {
    const result = await this.database.query<{
      input_snapshot: unknown;
      output_snapshot: unknown;
      has_input_snapshot: boolean;
      has_output_snapshot: boolean;
    }>(
      `
        SELECT
          input_snapshot,
          output_snapshot,
          input_snapshot IS NOT NULL AS has_input_snapshot,
          output_snapshot IS NOT NULL AS has_output_snapshot
        FROM step_execution
        WHERE id = $1 AND instance_id = $2
      `,
      [executionId, instanceId],
    );
    const row = result.rows[0];
    if (!row) {
      return null;
    }
    return {
      recordedInput: row.has_input_snapshot
        ? this.variableDocument(row.input_snapshot)
        : { status: "notRecorded" },
      recordedOutput: row.has_output_snapshot
        ? this.variableDocument(row.output_snapshot)
        : { status: "notRecorded" },
    };
  }

  private variableDocument(value: unknown): VariableDocument {
    const sizeBytes = Buffer.byteLength(JSON.stringify(value));
    return sizeBytes > this.maxJsonDocumentBytes
      ? { status: "contentTooLarge", sizeBytes }
      : { status: "present", value, sizeBytes };
  }
}
