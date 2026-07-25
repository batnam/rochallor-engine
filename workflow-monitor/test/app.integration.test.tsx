import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
    )
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

  fireEvent.click(
    screen.getByRole("link", { name: "browser-visible-instance" }),
  );

  expect(
    await screen.findByRole("heading", {
      name: "Process Instance browser-visible-instance",
    }),
  ).toBeVisible();
  expect(
    await screen.findByLabelText("Human Review, Current Token Position"),
  ).toBeVisible();
});
