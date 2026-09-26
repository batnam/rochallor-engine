import {
  Controller,
  Get,
  Inject,
  NotFoundException,
  Param,
  Query,
} from "@nestjs/common";

import {
  ApiExtraModels,
  ApiOkResponse,
  ApiQuery,
  getSchemaPath,
} from "@nestjs/swagger";
import { ExecutionContext } from "./execution-context";

import type {
  ProcessInstanceDetail,
  ProcessInstanceListItem,
  ProcessInstanceQuery,
  StepExecutionListItem,
  StepExecutionQuery,
} from "./dto/process-instance.dto";
import { ProcessInstanceQueries } from "./process-instance.queries";

@Controller("api/v1/process-instances")
@ApiExtraModels(ExecutionContext)
export class ProcessInstancesController {
  constructor(
    @Inject(ProcessInstanceQueries)
    private readonly queries: ProcessInstanceQueries,
  ) {}

  @Get()
  @ApiQuery({
    name: "definitionVersion",
    required: false,
    type: Number,
    minimum: 1,
    description: "Requires definitionId",
  })
  @ApiQuery({
    name: "currentStepId",
    required: false,
    type: String,
    description: "Requires definitionId; only ACTIVE/WAITING instances",
  })
  @ApiQuery({
    name: "stepStartedBefore",
    required: false,
    type: String,
    description:
      "Exclusive UTC cutoff on latest RUNNING execution; requires currentStepId",
  })
  list(@Query() query: ProcessInstanceQuery): Promise<{
    items: ProcessInstanceListItem[];
    nextCursor: string | null;
  }> {
    return this.queries.list(query);
  }

  @Get(":id")
  @ApiOkResponse({
    schema: {
      type: "object",
      required: [
        "instance",
        "definition",
        "executionOverlay",
        "observedAt",
        "executionContext",
      ],
      properties: {
        instance: { type: "object" },
        definition: { type: "object" },
        executionOverlay: { type: "object" },
        observedAt: { type: "string", format: "date-time" },
        executionContext: {
          type: "array",
          items: { $ref: getSchemaPath(ExecutionContext) },
        },
      },
    },
  })
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
