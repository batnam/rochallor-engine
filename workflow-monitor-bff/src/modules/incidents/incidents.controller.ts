import {
  Controller,
  Get,
  Inject,
  NotFoundException,
  Param,
  Query,
} from "@nestjs/common";

import type {
  IncidentDetail,
  IncidentListItem,
  IncidentQuery,
} from "./dto/incident.dto";
import { IncidentQueries } from "./incident.queries";

@Controller("api/v1/incidents")
export class IncidentsController {
  constructor(
    @Inject(IncidentQueries) private readonly queries: IncidentQueries,
  ) {}

  @Get()
  list(@Query() query: IncidentQuery): Promise<{
    items: IncidentListItem[];
    nextCursor: string | null;
  }> {
    return this.queries.list(query);
  }

  @Get(":id")
  async detail(@Param("id") id: string): Promise<{ incident: IncidentDetail }> {
    const incident = await this.queries.detail(id);
    if (!incident) {
      throw new NotFoundException("Incident not found");
    }
    return { incident };
  }
}
