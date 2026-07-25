import { Module } from "@nestjs/common";

import { ProcessInstanceQueries } from "./process-instance.queries";
import { ProcessInstancesController } from "./process-instances.controller";

@Module({
  controllers: [ProcessInstancesController],
  providers: [ProcessInstanceQueries],
})
export class ProcessInstancesModule {}
