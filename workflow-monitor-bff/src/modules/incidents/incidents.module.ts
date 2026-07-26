import { Module } from "@nestjs/common";

import { IncidentQueries } from "./incident.queries";
import { IncidentsController } from "./incidents.controller";

@Module({
  controllers: [IncidentsController],
  providers: [IncidentQueries],
})
export class IncidentsModule {}
