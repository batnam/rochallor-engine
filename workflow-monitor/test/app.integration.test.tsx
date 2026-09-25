import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterAll, beforeAll, expect, it } from "vitest";

import { createMonitorApp } from "../../workflow-monitor-bff/dist/app.js";
import {
  type PostgresFixture,
  startPostgresFixture,
} from "../../workflow-monitor-bff/test/support/postgres-fixture";
import { App } from "../src/App";

let app: Awaited<ReturnType<typeof createMonitorApp>> | undefined;
let postgres: PostgresFixture | undefined;
const originalFetch = globalThis.fetch;

beforeAll(async () => {
  postgres = await startPostgresFixture();
  await postgres.query(`
    INSERT INTO workflow_definition (
      id,
      version,
      name,
      raw_json,
      parsed_steps
    ) VALUES (
      'loan-approval',
      1,
      'Loan Approval',
      '{
        "id":"loan-approval",
        "name":"Loan Approval",
        "steps":[
          {"id":"human-review","name":"Human Review","type":"USER_TASK","nextStep":"end"},
          {"id":"end","name":"End","type":"END"}
        ]
      }',
      '[]'
    );

    INSERT INTO workflow_instance (
      id,
      definition_id,
      definition_version,
      status,
      current_step_ids,
      variables,
      started_at
    ) VALUES (
      'browser-visible-instance',
      'loan-approval',
      1,
      'WAITING',
      ARRAY['human-review'],
      '{"privateValue":"never-log-this"}',
      '2026-01-03T00:00:00Z'
    );
    INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at)
      VALUES ('review-attempt','browser-visible-instance','human-review','USER_TASK','RUNNING','2026-01-03T00:00:01Z');
    INSERT INTO user_task (id,instance_id,step_execution_id,step_id,assignee_group)
      VALUES ('review-task','browser-visible-instance','review-attempt','human-review','reviewers');
    INSERT INTO step_execution (id,instance_id,step_id,step_type,status,started_at,ended_at,input_snapshot,output_snapshot)
      VALUES ('prepare-attempt','browser-visible-instance','prepare','TRANSFORMATION','COMPLETED','2026-01-03T00:00:00Z','2026-01-03T00:00:01Z','{"a":1,"b":2}','{"a":3,"b":2}');
  `);

  app = await createMonitorApp({
    postgresDsn: postgres.readOnlyDsn,
    log: () => undefined,
  });
  await app.listen(0, "127.0.0.1");
  const address = app.getHttpServer().address();
  if (!address || typeof address === "string") {
    throw new Error("BFF did not listen on a TCP port");
  }
  const bffOrigin = `http://127.0.0.1:${address.port}`;
  globalThis.fetch = (input, init) =>
    originalFetch(new URL(String(input), bffOrigin), init);
});

afterAll(async () => {
  cleanup();
  globalThis.fetch = originalFetch;
  await app?.close();
  await postgres?.stop();
});

it("opens an execution diagram through the real BFF without an engine process", async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("cell", { name: "browser-visible-instance" }),
  ).toBeVisible();
  expect(screen.getByRole("cell", { name: "WAITING" })).toBeVisible();

  fireEvent.click(screen.getByRole("link", { name: "Overview" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Inspect loan-approval v1" }),
  );
  const step = await screen.findByRole("row", { name: "human-review 1 1 0" });
  fireEvent.click(within(step).getAllByRole("link")[1]);
  fireEvent.click(
    await screen.findByRole("link", { name: "browser-visible-instance" }),
  );

  expect(
    await screen.findByRole("heading", {
      name: "Process Instance browser-visible-instance",
    }),
  ).toBeVisible();
  expect(
    await screen.findByLabelText(
      "Human Review, Current Token Position, RUNNING, attempt 1",
    ),
  ).toBeVisible();
  expect(await screen.findByText(/Waiting for task completion/)).toBeVisible();
  expect(screen.getByText("reviewers")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Timeline" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Inspect attempt prepare-attempt",
    }),
  );
  const comparison = await screen.findByRole("region", {
    name: "Snapshot comparison",
  });
  expect(
    await within(comparison).findByRole("row", { name: "a changed 1 3" }),
  ).toBeVisible();
  queryClient.clear();
});
