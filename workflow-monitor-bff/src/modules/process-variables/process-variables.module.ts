import { type DynamicModule, Module } from "@nestjs/common";

import {
  MAX_JSON_DOCUMENT_BYTES,
  ProcessVariableQueries,
} from "./process-variable.queries";
import { ProcessVariablesController } from "./process-variables.controller";

@Module({})
class ProcessVariablesModule {}

export function registerProcessVariables(
  maxJsonDocumentBytes: number,
): DynamicModule {
  return {
    module: ProcessVariablesModule,
    controllers: [ProcessVariablesController],
    providers: [
      ProcessVariableQueries,
      {
        provide: MAX_JSON_DOCUMENT_BYTES,
        useValue: maxJsonDocumentBytes,
      },
    ],
  };
}
