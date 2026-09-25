import { Controller, Get, Inject, Query } from "@nestjs/common";
import { ApiOkResponse, ApiQuery } from "@nestjs/swagger";
import type { SchemaObject } from "@nestjs/swagger/dist/interfaces/open-api-spec.interface";
import { OverviewQueries, type OverviewQuery } from "./overview.queries";

const observation: SchemaObject = {
  type: "object",
  required: ["observedAt", "scope", "items", "nextCursor"],
  properties: {
    observedAt: { type: "string", format: "date-time" },
    scope: { type: "string" },
    nextCursor: { type: "string", nullable: true },
  },
};

@Controller("api/v1/overview")
export class OverviewController {
  constructor(
    @Inject(OverviewQueries) private readonly queries: OverviewQueries,
  ) {}

  @Get()
  @ApiQuery({
    name: "workflow",
    required: false,
    type: String,
    description:
      "Case-insensitive literal substring of workflow name or ID; surrounding whitespace is ignored.",
  })
  @ApiQuery({
    name: "pageSize",
    required: false,
    type: Number,
    maximum: 100,
    minimum: 1,
  })
  @ApiQuery({ name: "cursor", required: false, type: String })
  @ApiOkResponse({
    schema: {
      ...observation,
      properties: {
        ...observation.properties,
        items: {
          type: "array",
          items: {
            type: "object",
            properties: {
              definitionId: { type: "string" },
              definitionVersion: { type: "integer" },
              name: { type: "string" },
              active: { type: "integer" },
              waiting: { type: "integer" },
              failed: { type: "integer" },
            },
          },
        },
      },
    },
  })
  definitions(@Query() query: OverviewQuery) {
    return this.queries.definitions(query);
  }

  @Get("steps")
  @ApiQuery({ name: "definitionId", type: String })
  @ApiQuery({ name: "definitionVersion", type: Number, minimum: 1 })
  @ApiQuery({
    name: "minStepAgeSeconds",
    required: false,
    type: Number,
    minimum: 1,
    example: 1800,
  })
  @ApiQuery({
    name: "pageSize",
    required: false,
    type: Number,
    maximum: 100,
    minimum: 1,
  })
  @ApiQuery({ name: "cursor", required: false, type: String })
  @ApiOkResponse({
    schema: {
      ...observation,
      properties: {
        ...observation.properties,
        definitionId: { type: "string" },
        definitionVersion: { type: "integer" },
        minStepAgeSeconds: { type: "integer" },
        stepStartedBefore: {
          type: "string",
          format: "date-time",
          description: "Exclusive age cutoff for drill-down",
        },
        items: {
          type: "array",
          items: {
            type: "object",
            properties: {
              stepId: { type: "string" },
              instances: { type: "integer" },
              aged: { type: "integer" },
              ageUnavailable: { type: "integer" },
            },
          },
        },
      },
    },
  })
  steps(@Query() query: OverviewQuery) {
    return this.queries.steps(query);
  }
}
