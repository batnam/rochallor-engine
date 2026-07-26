import type { INestApplication } from "@nestjs/common";
import request from "supertest";

import { createMonitorApp } from "../src/app";

describe("OpenAPI HTTP seam", () => {
  let app: INestApplication;

  beforeAll(async () => {
    app = await createMonitorApp();
    await app.init();
  });

  afterAll(async () => {
    await app.close();
  });

  it("documents only read operations", async () => {
    const response = await request(app.getHttpServer())
      .get("/openapi.json")
      .expect(200);

    expect(response.body.paths).toEqual(
      expect.objectContaining({
        "/api/v1/incidents": { get: expect.any(Object) },
        "/api/v1/incidents/{id}": { get: expect.any(Object) },
        "/api/v1/process-instances": { get: expect.any(Object) },
        "/api/v1/process-instances/{id}": { get: expect.any(Object) },
        "/api/v1/workflow-definitions": { get: expect.any(Object) },
        "/health/live": { get: expect.any(Object) },
        "/health/ready": { get: expect.any(Object) },
      }),
    );

    const methods = Object.values(
      response.body.paths as Record<string, Record<string, unknown>>,
    ).flatMap((operations) => Object.keys(operations));
    expect(new Set(methods)).toEqual(new Set(["get"]));
  });

  it("documents present, not-recorded, and oversized Variable Documents explicitly", async () => {
    const response = await request(app.getHttpServer())
      .get("/openapi.json")
      .expect(200);

    expect(response.body.components.schemas).toEqual(
      expect.objectContaining({
        PresentVariableDocument: expect.objectContaining({
          required: ["status", "value", "sizeBytes"],
          properties: expect.objectContaining({
            status: { type: "string", enum: ["present"] },
            value: {
              nullable: true,
              oneOf: [
                { type: "object", additionalProperties: true },
                { type: "array", items: {} },
                { type: "string" },
                { type: "number" },
                { type: "boolean" },
              ],
            },
            sizeBytes: { type: "integer" },
          }),
        }),
        NotRecordedVariableDocument: expect.objectContaining({
          required: ["status"],
          properties: {
            status: { type: "string", enum: ["notRecorded"] },
          },
        }),
        ContentTooLargeVariableDocument: expect.objectContaining({
          required: ["status", "sizeBytes"],
          properties: {
            status: { type: "string", enum: ["contentTooLarge"] },
            sizeBytes: { type: "integer" },
          },
        }),
      }),
    );

    const currentSchema =
      response.body.paths["/api/v1/process-instances/{id}/variables"].get
        .responses["200"].content["application/json"].schema.properties.current;
    const snapshotsSchema =
      response.body.paths[
        "/api/v1/process-instances/{id}/step-executions/{executionId}/variables"
      ].get.responses["200"].content["application/json"].schema.properties;

    const expectedVariants = [
      { $ref: "#/components/schemas/PresentVariableDocument" },
      { $ref: "#/components/schemas/NotRecordedVariableDocument" },
      { $ref: "#/components/schemas/ContentTooLargeVariableDocument" },
    ];
    expect(currentSchema.oneOf).toEqual(expectedVariants);
    expect(snapshotsSchema.recordedInput.oneOf).toEqual(expectedVariants);
    expect(snapshotsSchema.recordedOutput.oneOf).toEqual(expectedVariants);
  });
});
