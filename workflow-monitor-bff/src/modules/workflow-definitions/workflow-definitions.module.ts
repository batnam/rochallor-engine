import { Module } from "@nestjs/common";

import { WorkflowDefinitionsController } from "./workflow-definitions.controller";

@Module({
  controllers: [WorkflowDefinitionsController],
})
export class WorkflowDefinitionsModule {}
