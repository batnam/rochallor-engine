import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { VariableSnapshotInspector } from "../src/process-variables/VariableSnapshotInspector";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());

it("does not fetch or show an endless loading state for an inspected attempt with no snapshots", () => {
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <VariableSnapshotInspector
        instanceId="i"
        execution={{
          id: "empty",
          stepId: "archive",
          attemptNumber: 1,
          status: "COMPLETED",
          hasInputSnapshot: false,
          hasOutputSnapshot: false,
        }}
        initiallyExpanded
      />
    </QueryClientProvider>,
  );
  expect(screen.getAllByText("Not recorded")).toHaveLength(2);
  expect(screen.getByRole("heading", { name: "archive" })).toBeVisible();
  expect(
    screen.queryByText("Loading Variable Snapshots…"),
  ).not.toBeInTheDocument();
  expect(client.isFetching()).toBe(0);
  client.clear();
});

it("refreshes an inspected running attempt when output arrives, retaining the input if that refresh fails", async () => {
  let calls = 0;
  let fail = false;
  let completed = false;
  server.use(
    http.get("/api/v1/process-instances/i/step-executions/e/variables", () => {
      calls++;
      if (fail) return new HttpResponse(null, { status: 503 });
      return HttpResponse.json({
        outputInterpretation: "variableDelta",
        recordedInput: { status: "present", value: { a: 1 }, sizeBytes: 7 },
        recordedOutput: completed
          ? { status: "present", value: { a: 3 }, sizeBytes: 7 }
          : { status: "notRecorded" },
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (status: string, hasOutputSnapshot: boolean) => (
    <QueryClientProvider client={client}>
      <VariableSnapshotInspector
        instanceId="i"
        stepName="Check Risk"
        execution={{
          id: "e",
          stepId: "check-risk",
          attemptNumber: 2,
          status,
          hasInputSnapshot: true,
          hasOutputSnapshot,
        }}
        initiallyExpanded
      />
    </QueryClientProvider>
  );
  const mounted = render(view("RUNNING", false));
  expect(screen.getByRole("heading", { name: "Check Risk" })).toBeVisible();
  expect(screen.getByText("Attempt 2")).toBeVisible();
  await screen.findByText("Recorded Output: Not recorded");
  fail = true;
  mounted.rerender(view("COMPLETED", true));
  await waitFor(() => expect(calls).toBe(2));
  expect(await screen.findByText("Stale Variable Snapshot data")).toBeVisible();
  expect(screen.getByRole("heading", { name: "Recorded Input" })).toBeVisible();
  fail = false;
  completed = true;
  fireEvent.click(screen.getByRole("button", { name: "Retry snapshots" }));
  expect(
    await screen.findByRole("row", { name: "a changed 1 3" }),
  ).toBeVisible();
  client.clear();
});
