import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";

import { ProcessInstanceRoute } from "../src/routes/ProcessInstanceRoute";

const server = setupServer();
const clients: QueryClient[] = [];

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterAll(() => server.close());
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  server.resetHandlers();
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
  vi.useRealTimers();
});

function start(initialStatus = "ACTIVE") {
  const state = {
    status: initialStatus,
    failDetail: false,
    failHistory: false,
    failVariables: false,
    detailRequests: 0,
    historyRequests: 0,
    variableRequests: 0,
  };
  server.use(
    http.get("/api/v1/process-instances/live", () => {
      state.detailRequests += 1;
      if (state.failDetail) return new HttpResponse(null, { status: 503 });
      return HttpResponse.json({
        instance: {
          id: "live",
          status: state.status,
          definitionId: "flow",
          definitionVersion: 1,
          businessKey: "order-1",
        },
        definition: {
          id: "flow",
          version: 1,
          name: "Flow",
          steps: [{ id: "work", name: "Work", type: "SERVICE_TASK" }],
        },
        executionOverlay: {
          currentTokenStepIds: ["ACTIVE", "WAITING"].includes(state.status)
            ? ["work"]
            : [],
          failedStepId: state.status === "FAILED" ? "work" : null,
          latestByStep: [],
        },
      });
    }),
    http.get("/api/v1/process-instances/live/step-executions", () => {
      state.historyRequests += 1;
      if (state.failHistory) return new HttpResponse(null, { status: 503 });
      return HttpResponse.json({
        items: [
          {
            id: "attempt-1",
            stepId: "work",
            stepType: "SERVICE_TASK",
            attemptNumber: 1,
            status: ["ACTIVE", "WAITING"].includes(state.status)
              ? "RUNNING"
              : state.status,
            startedAt: "2026-01-01T00:00:00Z",
            endedAt: null,
            hasFailure: state.status === "FAILED",
            hasInputSnapshot: false,
            hasOutputSnapshot: false,
          },
        ],
        nextCursor: null,
      });
    }),
    http.get("/api/v1/process-instances/live/variables", () => {
      state.variableRequests += 1;
      if (state.failVariables) return new HttpResponse(null, { status: 503 });
      return HttpResponse.json({
        current: { status: "present", value: { order: 1 }, sizeBytes: 11 },
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <ProcessInstanceRoute instanceId="live" search="" />
    </QueryClientProvider>,
  );
  return { state, client };
}

it("refreshes status, diagram and history from the same manual action", async () => {
  const { state } = start();
  await screen.findByLabelText("Work, Current Token Position");
  await screen.findByRole("cell", { name: "RUNNING" });
  state.status = "COMPLETED";
  fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
  await screen.findByRole("cell", { name: "COMPLETED" });
  await vi.waitFor(() => expect(state.detailRequests).toBe(2));
  expect(
    screen.queryByLabelText("Work, Current Token Position"),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/Status and diagram updated/)).toBeVisible();
});

it.each([
  ["ACTIVE", "COMPLETED"],
  ["WAITING", "FAILED"],
  ["ACTIVE", "CANCELLED"],
])(
  "polls %s through %s, then stops after refreshing history",
  async (initial, terminal) => {
    vi.useFakeTimers();
    const { state } = start(initial);
    await vi.waitFor(() =>
      expect(screen.getByRole("cell", { name: "RUNNING" })).toBeVisible(),
    );
    state.status = terminal;
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    await vi.waitFor(() =>
      expect(screen.getByRole("cell", { name: terminal })).toBeVisible(),
    );
    expect(state.detailRequests).toBe(2);
    expect(state.historyRequests).toBe(2);
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(state.detailRequests).toBe(2);
    expect(state.historyRequests).toBe(2);
  },
);

it("retains detail after a failed refetch and recovers on the next refresh", async () => {
  const { state, client } = start();
  await screen.findByLabelText("Work, Current Token Position");
  state.failDetail = true;
  await act(() =>
    client.invalidateQueries({ queryKey: ["process-instance", "live"] }),
  );
  await screen.findByText("Stale Process Instance data");
  expect(
    screen.getByRole("heading", { name: "Process Instance live" }),
  ).toBeVisible();
  expect(screen.getByLabelText("Work, Current Token Position")).toBeVisible();
  expect(screen.getByText("Stale Process Instance data")).toBeVisible();
  state.failDetail = false;
  fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
  await vi.waitFor(() =>
    expect(
      screen.queryByText("Stale Process Instance data"),
    ).not.toBeInTheDocument(),
  );
});

it("retries history if the final cycle only partially succeeds", async () => {
  vi.useFakeTimers();
  const { state } = start();
  await vi.waitFor(() =>
    expect(screen.getByRole("cell", { name: "RUNNING" })).toBeVisible(),
  );
  state.status = "COMPLETED";
  state.failHistory = true;
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await vi.waitFor(() =>
    expect(screen.getByText("Stale Step Execution data")).toBeVisible(),
  );
  expect(screen.getByRole("cell", { name: "RUNNING" })).toBeVisible();
  state.failHistory = false;
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await vi.waitFor(() =>
    expect(screen.getByRole("cell", { name: "COMPLETED" })).toBeVisible(),
  );
});

it("pauses hidden polling and refreshes all visible data on return", async () => {
  vi.useFakeTimers();
  const { state } = start();
  await vi.waitFor(() => expect(state.historyRequests).toBe(1));
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "hidden",
  });
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(state.detailRequests).toBe(1);
  expect(state.historyRequests).toBe(1);
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  await vi.waitFor(() => expect(state.detailRequests).toBe(2));
  expect(state.historyRequests).toBe(2);
});

it("retains variables with a stale warning and only refreshes them while visible", async () => {
  const { state } = start();
  await screen.findByRole("heading", { name: "Process Instance live" });
  expect(state.variableRequests).toBe(0);
  fireEvent.click(screen.getByRole("tab", { name: "Variables" }));
  await screen.findByRole("row", { name: "order number 1" });
  state.failVariables = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText("Stale Current Variable data");
  expect(screen.getByRole("row", { name: "order number 1" })).toBeVisible();
  expect(state.detailRequests).toBe(2);
  expect(state.historyRequests).toBe(2);
  fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await vi.waitFor(() => expect(state.detailRequests).toBe(3));
  expect(state.variableRequests).toBe(2);
});
