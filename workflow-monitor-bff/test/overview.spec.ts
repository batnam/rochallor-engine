import type { INestApplication } from "@nestjs/common";
import request from "supertest";
import { createMonitorApp } from "../src/app";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("Overview and exact drill-down", () => {
  let app: INestApplication;
  let postgres: PostgresFixture;
  beforeAll(async () => {
    postgres = await startPostgresFixture();
    await postgres.query(`
      INSERT INTO workflow_definition (id,version,name,raw_json,parsed_steps) VALUES
        ('flow',1,'Flow one','{}','[]'),('flow',2,'Flow two','{}','[]');
      INSERT INTO workflow_instance (id,definition_id,definition_version,status,current_step_ids,started_at) VALUES
        ('old','flow',1,'ACTIVE',ARRAY['work','wait','join','work'],'2000-01-01'),
        ('recent','flow',1,'WAITING',ARRAY['work'],now()),
        ('missing','flow',1,'ACTIVE',ARRAY['work'],now()),
        ('failed','flow',1,'FAILED',ARRAY['work'],now()),
        ('done','flow',1,'COMPLETED',ARRAY['work'],now()),
        ('cancelled','flow',1,'CANCELLED',ARRAY['work'],now()),
        ('v2','flow',2,'ACTIVE',ARRAY['work'],'2000-01-01');
      INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at,attempt_number) VALUES
        ('old-failure','old','work','SERVICE_TASK','FAILED','1999-01-01',1),
        ('work','old','work','SERVICE_TASK','RUNNING','2000-01-01',2),
        ('wait','old','wait','WAIT','RUNNING','2000-01-01',1),
        ('join','old','join','JOIN_GATEWAY','COMPLETED','2000-01-01',1),
        ('recent','recent','work','SERVICE_TASK','RUNNING',now(),1),
        ('failed','failed','work','SERVICE_TASK','FAILED',now(),1),
        ('v2','v2','work','SERVICE_TASK','RUNNING','2000-01-01',1);
    `);
    app = await createMonitorApp({
      postgresDsn: postgres.readOnlyDsn,
      log: () => undefined,
    });
    await app.init();
  });
  afterAll(async () => {
    await app?.close();
    await postgres?.stop();
  });
  const selection = { definitionId: "flow", definitionVersion: "1" };

  it("counts retained instances once per status/version, including old instances", async () => {
    const result = await request(app.getHttpServer())
      .get("/api/v1/overview")
      .expect(200);
    expect(result.body.items).toEqual([
      {
        definitionId: "flow",
        definitionVersion: 1,
        name: "Flow one",
        active: 2,
        waiting: 1,
        failed: 1,
      },
      {
        definitionId: "flow",
        definitionVersion: 2,
        name: "Flow two",
        active: 1,
        waiting: 0,
        failed: 0,
      },
    ]);
    expect(Date.parse(result.body.observedAt)).not.toBeNaN();
  });

  it("counts distinct current instances, latest running ages and unavailable ages", async () => {
    const result = await request(app.getHttpServer())
      .get("/api/v1/overview/steps")
      .query(selection)
      .expect(200);
    expect(result.body.items).toEqual([
      { stepId: "join", instances: 1, aged: 0, ageUnavailable: 1 },
      { stepId: "wait", instances: 1, aged: 1, ageUnavailable: 0 },
      { stepId: "work", instances: 3, aged: 1, ageUnavailable: 1 },
    ]);
    expect(
      Date.parse(result.body.observedAt) -
        Date.parse(result.body.stepStartedBefore),
    ).toBe(1800000);
    for (const row of result.body.items) {
      const all = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ ...selection, currentStepId: row.stepId })
        .expect(200);
      expect(all.body.items).toHaveLength(row.instances);
      const aged = await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({
          ...selection,
          currentStepId: row.stepId,
          stepStartedBefore: result.body.stepStartedBefore,
        })
        .expect(200);
      expect(aged.body.items).toHaveLength(row.aged);
    }
  });

  it("uses an exclusive cutoff and never counts an old failed attempt as the current age", async () => {
    const query = {
      ...selection,
      currentStepId: "work",
      stepStartedBefore: "2000-01-01T00:00:00Z",
    };
    const equal = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query(query)
      .expect(200);
    expect(equal.body.items).toEqual([]);
    const later = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query({ ...query, stepStartedBefore: "2000-01-01T00:00:01Z" })
      .expect(200);
    expect(later.body.items.map((row: { id: string }) => row.id)).toEqual([
      "old",
    ]);
  });

  it("paginates groups with cursors bound to the selected version and threshold", async () => {
    const first = await request(app.getHttpServer())
      .get("/api/v1/overview")
      .query({ pageSize: 1 })
      .expect(200);
    const second = await request(app.getHttpServer())
      .get("/api/v1/overview")
      .query({ pageSize: 1, cursor: first.body.nextCursor })
      .expect(200);
    expect(second.body.items[0].definitionVersion).toBe(2);
    expect(second.body.nextCursor).toBeNull();
    const step = await request(app.getHttpServer())
      .get("/api/v1/overview/steps")
      .query({ ...selection, pageSize: 1 })
      .expect(200);
    expect(step.body.items[0].stepId).toBe("join");
    const next = await request(app.getHttpServer())
      .get("/api/v1/overview/steps")
      .query({ ...selection, pageSize: 1, cursor: step.body.nextCursor })
      .expect(200);
    expect(next.body.items[0].stepId).toBe("wait");
    for (const changes of [
      { definitionVersion: "2" },
      { minStepAgeSeconds: "60" },
    ]) {
      await request(app.getHttpServer())
        .get("/api/v1/overview/steps")
        .query({ ...selection, ...changes, cursor: step.body.nextCursor })
        .expect(400);
    }
    await request(app.getHttpServer())
      .get("/api/v1/overview/steps")
      .query({ ...selection, cursor: first.body.nextCursor })
      .expect(400);
  });

  it("binds instance cursors to all new drill-down filters", async () => {
    const query = { ...selection, currentStepId: "work", pageSize: 1 };
    const first = await request(app.getHttpServer())
      .get("/api/v1/process-instances")
      .query(query)
      .expect(200);
    for (const change of [
      { definitionVersion: "2" },
      { currentStepId: "wait" },
      { stepStartedBefore: "2026-01-01T00:00:00Z" },
    ]) {
      await request(app.getHttpServer())
        .get("/api/v1/process-instances")
        .query({ ...query, ...change, cursor: first.body.nextCursor })
        .expect(400);
    }
  });

  it.each([
    ["/overview", { pageSize: "101" }],
    ["/overview", { cursor: "bad" }],
    [
      "/overview",
      {
        cursor: Buffer.from(
          JSON.stringify({
            v: 1,
            scope: "definitions",
            id: "flow",
            version: 2147483648,
          }),
        ).toString("base64url"),
      },
    ],
    ["/overview/steps", {}],
    ["/overview/steps", { ...selection, minStepAgeSeconds: "-1" }],
    ["/overview/steps", { ...selection, minStepAgeSeconds: "1.5" }],
    ["/overview/steps", { ...selection, definitionVersion: "2147483648" }],
    ["/process-instances", { definitionVersion: "1" }],
    ["/process-instances", { currentStepId: "work" }],
    [
      "/process-instances",
      { ...selection, stepStartedBefore: "2026-01-01T00:00:00Z" },
    ],
    [
      "/process-instances",
      { ...selection, currentStepId: "work", stepStartedBefore: "invalid" },
    ],
  ])("rejects invalid %s filters %j", async (path, query) => {
    await request(app.getHttpServer())
      .get(`/api/v1${path}`)
      .query(query)
      .expect(400);
  });

  it("returns empty data for an unknown definition and treats SQL-looking input as a value", async () => {
    const result = await request(app.getHttpServer())
      .get("/api/v1/overview/steps")
      .query({ ...selection, definitionId: "' OR 1=1 --" })
      .expect(200);
    expect(result.body.items).toEqual([]);
    expect(result.body.observedAt).toEqual(expect.any(String));
  });

  describe("workflow search", () => {
    beforeAll(async () => {
      await postgres.query(`
        INSERT INTO workflow_definition (id,version,name,raw_json,parsed_steps) VALUES
          ('a-billing',1,'Billing','{}','[]'),
          ('z-checkout',1,'Shopping cart','{}','[]'),
          ('z-checkout',2,'New shopping cart','{}','[]');
        INSERT INTO workflow_instance (id,definition_id,definition_version,status) VALUES
          ('billing','a-billing',1,'ACTIVE'),
          ('cart-v1','z-checkout',1,'WAITING'),
          ('cart-v2','z-checkout',2,'ACTIVE');
      `);
    });

    it.each([" CHECKout ", "SHOPPING CART"])(
      "finds partial names and IDs across pages for %j",
      async (workflow) => {
        const first = await request(app.getHttpServer())
          .get("/api/v1/overview")
          .query({ workflow, pageSize: 1 })
          .expect(200);
        expect(first.body.items).toEqual([
          {
            definitionId: "z-checkout",
            definitionVersion: 1,
            name: "Shopping cart",
            active: 0,
            waiting: 1,
            failed: 0,
          },
        ]);
        expect(first.body.nextCursor).toEqual(expect.any(String));
        const second = await request(app.getHttpServer())
          .get("/api/v1/overview")
          .query({ workflow, pageSize: 1, cursor: first.body.nextCursor })
          .expect(200);
        expect(second.body.items).toEqual([
          {
            definitionId: "z-checkout",
            definitionVersion: 2,
            name: "New shopping cart",
            active: 1,
            waiting: 0,
            failed: 0,
          },
        ]);
        expect(second.body.nextCursor).toBeNull();
        for (const otherWorkflow of ["billing", ""]) {
          await request(app.getHttpServer())
            .get("/api/v1/overview")
            .query({ workflow: otherWorkflow, cursor: first.body.nextCursor })
            .expect(400);
        }
      },
    );

    it("matches each version's own name", async () => {
      const result = await request(app.getHttpServer())
        .get("/api/v1/overview")
        .query({ workflow: "new shopping" })
        .expect(200);
      expect(result.body.items).toEqual([
        expect.objectContaining({
          definitionId: "z-checkout",
          definitionVersion: 2,
        }),
      ]);
    });

    it.each(["missing", "%", "_", "\\", "' OR 1=1 --"])(
      "treats %j as literal search text",
      async (workflow) => {
        const result = await request(app.getHttpServer())
          .get("/api/v1/overview")
          .query({ workflow })
          .expect(200);
        expect(result.body.items).toEqual([]);
        expect(result.body.nextCursor).toBeNull();
      },
    );

    it("treats whitespace as no filter and rejects repeated search parameters", async () => {
      const all = await request(app.getHttpServer())
        .get("/api/v1/overview")
        .expect(200);
      const blank = await request(app.getHttpServer())
        .get("/api/v1/overview")
        .query({ workflow: "   " })
        .expect(200);
      expect(blank.body.items).toEqual(all.body.items);
      await request(app.getHttpServer())
        .get("/api/v1/overview?workflow=flow&workflow=cart")
        .expect(400);
    });
  });
});
