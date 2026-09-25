import {
  Controller,
  Get,
  Inject,
  NotFoundException,
  Param,
  Query,
} from "@nestjs/common";

import { ApiOkResponse } from "@nestjs/swagger";
import type { SchemaObject } from "@nestjs/swagger/dist/interfaces/open-api-spec.interface";

import type {
  IncidentDetail,
  IncidentListItem,
  IncidentQuery,
} from "./dto/incident.dto";
import { IncidentQueries } from "./incident.queries";

const incidentSchema: SchemaObject = {
  type: "object",
  required: ["id", "historical", "latestAttempt"],
  properties: {
    id: { type: "string" },
    historical: {
      type: "boolean",
      description:
        "Whether a later step attempt exists; not a persisted resolution state.",
    },
    latestAttempt: {
      type: "object",
      required: ["executionId", "status", "attemptNumber"],
      properties: {
        executionId: { type: "string" },
        status: { type: "string" },
        attemptNumber: { type: "integer" },
      },
    },
  },
};

@Controller("api/v1/incidents")
export class IncidentsController {
  constructor(
    @Inject(IncidentQueries) private readonly queries: IncidentQueries,
  ) {}

  @Get()
  @ApiOkResponse({
    schema: {
      type: "object",
      properties: {
        items: { type: "array", items: incidentSchema },
        nextCursor: { type: "string", nullable: true },
      },
    },
  })
  list(@Query() query: IncidentQuery): Promise<{
    items: IncidentListItem[];
    nextCursor: string | null;
  }> {
    return this.queries.list(query);
  }

  @Get(":id")
  @ApiOkResponse({
    schema: { type: "object", properties: { incident: incidentSchema } },
  })
  async detail(@Param("id") id: string): Promise<{ incident: IncidentDetail }> {
    const incident = await this.queries.detail(id);
    if (!incident) {
      throw new NotFoundException("Incident not found");
    }
    return { incident };
  }
}
