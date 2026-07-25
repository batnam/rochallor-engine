import {
  Controller,
  Get,
  Inject,
  NotFoundException,
  Param,
  Query,
} from "@nestjs/common";

import type {
  ProcessInstanceDetail,
  ProcessInstanceListItem,
  ProcessInstanceQuery,
  StepExecutionListItem,
  StepExecutionQuery,
} from "./dto/process-instance.dto";
import { ProcessInstanceQueries } from "./process-instance.queries";

@Controller("api/v1/process-instances")
export class ProcessInstancesController {
  constructor(
    @Inject(ProcessInstanceQueries)
    private readonly queries: ProcessInstanceQueries,
  ) {}

  @Get()
  list(@Query() query: ProcessInstanceQuery): Promise<{
    items: ProcessInstanceListItem[];
    nextCursor: string | null;
  }> {
    return this.queries.list(query);
  }

  @Get(":id")
  async detail(@Param("id") id: string): Promise<ProcessInstanceDetail> {
    const detail = await this.queries.detail(id);
    if (!detail) {
      throw new NotFoundException("Process Instance not found");
    }
    return detail;
  }

  @Get(":id/step-executions")
  stepExecutions(
    @Param("id") id: string,
    @Query() query: StepExecutionQuery,
  ): Promise<{
    items: StepExecutionListItem[];
    nextCursor: string | null;
  }> {
    return this.queries.listStepExecutions(id, query);
  }
}
