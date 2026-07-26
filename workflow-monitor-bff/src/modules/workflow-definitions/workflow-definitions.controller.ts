import { Controller, Get, Inject } from "@nestjs/common";

import { MonitorDatabase } from "../../common/database/monitor-database";
import type { WorkflowDefinitionOption } from "./dto/workflow-definition.dto";

@Controller("api/v1/workflow-definitions")
export class WorkflowDefinitionsController {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
  ) {}

  @Get()
  async list(): Promise<{ items: WorkflowDefinitionOption[] }> {
    const result = await this.database.query<WorkflowDefinitionOption>(`
      SELECT DISTINCT ON (id) id, name
      FROM workflow_definition
      ORDER BY id, version DESC
    `);
    return { items: result.rows };
  }
}
