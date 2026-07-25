import {
  Controller,
  Get,
  Inject,
  NotFoundException,
  Param,
} from "@nestjs/common";
import { ApiExtraModels, ApiOkResponse, getSchemaPath } from "@nestjs/swagger";

import {
  ContentTooLargeVariableDocument,
  NotRecordedVariableDocument,
  PresentVariableDocument,
  type VariableDocument,
} from "./dto/variable-document.dto";
import { ProcessVariableQueries } from "./process-variable.queries";

const variableDocumentSchema = {
  oneOf: [
    { $ref: getSchemaPath(PresentVariableDocument) },
    { $ref: getSchemaPath(NotRecordedVariableDocument) },
    { $ref: getSchemaPath(ContentTooLargeVariableDocument) },
  ],
};

@Controller("api/v1/process-instances")
@ApiExtraModels(
  PresentVariableDocument,
  NotRecordedVariableDocument,
  ContentTooLargeVariableDocument,
)
export class ProcessVariablesController {
  constructor(
    @Inject(ProcessVariableQueries)
    private readonly queries: ProcessVariableQueries,
  ) {}

  @Get(":id/variables")
  @ApiOkResponse({
    schema: {
      type: "object",
      required: ["current"],
      properties: {
        current: variableDocumentSchema,
      },
    },
  })
  async current(
    @Param("id") id: string,
  ): Promise<{ current: VariableDocument }> {
    const current = await this.queries.current(id);
    if (!current) {
      throw new NotFoundException("Process Instance not found");
    }
    return { current };
  }

  @Get(":id/step-executions/:executionId/variables")
  @ApiOkResponse({
    schema: {
      type: "object",
      required: ["recordedInput", "recordedOutput"],
      properties: {
        recordedInput: variableDocumentSchema,
        recordedOutput: variableDocumentSchema,
      },
    },
  })
  async recorded(
    @Param("id") id: string,
    @Param("executionId") executionId: string,
  ): Promise<{
    recordedInput: VariableDocument;
    recordedOutput: VariableDocument;
  }> {
    const variables = await this.queries.recorded(id, executionId);
    if (!variables) {
      throw new NotFoundException("Step Execution not found");
    }
    return variables;
  }
}
