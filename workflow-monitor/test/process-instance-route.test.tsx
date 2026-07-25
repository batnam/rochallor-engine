import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";

import { ProcessInstanceRoute } from "../src/routes/ProcessInstanceRoute";
import { RouteHarness } from "./route-harness";

function ProcessInstanceTestRoute(): ReactNode {
  return (
    <RouteHarness>
      {(location) => {
        const match = /^\/process-instances\/([^/]+)$/.exec(location.pathname);
        if (!match) {
          throw new Error("Process Instance route is required");
        }
        return (
          <ProcessInstanceRoute
            instanceId={decodeURIComponent(match[1])}
            search={location.search}
          />
        );
      }}
    </RouteHarness>
  );
}

const server = setupServer(
  http.get("/api/v1/process-instances", () =>
    HttpResponse.json({
      items: [
        {
          id: "instance-failed",
          definitionId: "loan-approval",
          definitionVersion: 2,
          status: "FAILED",
          currentStepIds: ["check-risk"],
          startedAt: "2026-01-02T00:00:00.000Z",
          completedAt: "2026-01-02T00:05:00.000Z",
          failureReason: "worker failed",
          businessKey: "loan-002",
        },
        {
          id: "instance-active",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "ACTIVE",
          currentStepIds: ["collect-documents"],
          startedAt: "2026-01-01T00:00:00.000Z",
          completedAt: null,
          failureReason: null,
          businessKey: "loan-001",
        },
      ],
      nextCursor: null,
    }),
  ),
  http.get("/api/v1/workflow-definitions", () =>
    HttpResponse.json({
      items: [{ id: "loan-approval", name: "Loan Approval" }],
    }),
  ),
  http.get("/api/v1/process-instances/:instanceId/step-executions", () =>
    HttpResponse.json({ items: [], nextCursor: null }),
  ),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  window.history.replaceState(null, "", "/");
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
  vi.useRealTimers();
});
afterAll(() => server.close());

it("renders a Current Token Position on a read-only execution diagram", async () => {
  window.history.replaceState(null, "", "/process-instances/graph-active");
  server.use(
    http.get("/api/v1/process-instances/graph-active", () =>
      HttpResponse.json({
        instance: {
          id: "graph-active",
          definitionId: "graph-flow",
          definitionVersion: 1,
          status: "ACTIVE",
          currentStepIds: ["collect-documents"],
          businessKey: null,
        },
        definition: {
          id: "graph-flow",
          version: 1,
          name: "Graph Flow",
          steps: [
            {
              id: "collect-documents",
              name: "Collect Documents",
              type: "USER_TASK",
              nextStep: "end",
            },
            { id: "end", name: "End", type: "END" },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: ["collect-documents"],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
  );
  const queryClient = new QueryClient();

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("group", { name: "Execution Diagram" }),
  ).toBeVisible();
  expect(
    screen.getByLabelText("Collect Documents, Current Token Position"),
  ).toBeVisible();
  expect(screen.getByLabelText("End")).toBeVisible();
  expect(
    screen.getByLabelText("Collect Documents to End, sequential edge"),
  ).toBeVisible();
});

it("shows every Step Execution attempt without snapshot content", async () => {
  window.history.replaceState(null, "", "/process-instances/history-instance");
  server.use(
    http.get("/api/v1/process-instances/history-instance", () =>
      HttpResponse.json({
        instance: {
          id: "history-instance",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "WAITING",
          currentStepIds: ["check-risk"],
          businessKey: "loan-001",
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [
            {
              id: "check-risk",
              name: "Check Risk",
              type: "SERVICE_TASK",
            },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: ["check-risk"],
          failedStepId: null,
          latestByStep: [
            {
              executionId: "risk-attempt-2",
              stepId: "check-risk",
              status: "RUNNING",
              attemptNumber: 2,
            },
          ],
        },
      }),
    ),
    http.get("/api/v1/process-instances/history-instance/step-executions", () =>
      HttpResponse.json({
        items: [
          {
            id: "risk-attempt-2",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 2,
            status: "RUNNING",
            startedAt: "2026-04-01T00:00:03.000Z",
            endedAt: null,
            hasFailure: false,
            hasInputSnapshot: true,
            hasOutputSnapshot: true,
          },
          {
            id: "risk-attempt-1",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 1,
            status: "FAILED",
            startedAt: "2026-04-01T00:00:01.000Z",
            endedAt: "2026-04-01T00:00:02.000Z",
            hasFailure: true,
            hasInputSnapshot: true,
            hasOutputSnapshot: false,
          },
        ],
        nextCursor: null,
      }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("heading", { name: "Step Executions" }),
  ).toBeVisible();
  expect(screen.getByRole("cell", { name: "risk-attempt-2" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "risk-attempt-1" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "RUNNING" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "FAILED" })).toBeVisible();
  expect(screen.getAllByRole("cell", { name: "Recorded" })).toHaveLength(3);
  expect(screen.getByRole("cell", { name: "Not recorded" })).toBeVisible();
  expect(screen.queryByText("sensitive-input")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /retry|cancel|edit/i }),
  ).not.toBeInTheDocument();
});

it("makes graph-node selection clear and highlights matching attempts", async () => {
  window.history.replaceState(
    null,
    "",
    "/process-instances/correlation-instance",
  );
  server.use(
    http.get("/api/v1/process-instances/correlation-instance", () =>
      HttpResponse.json({
        instance: {
          id: "correlation-instance",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "COMPLETED",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [
            {
              id: "check-risk",
              name: "Check Risk",
              type: "SERVICE_TASK",
              nextStep: "approve-loan",
            },
            {
              id: "approve-loan",
              name: "Approve Loan",
              type: "USER_TASK",
            },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [
            {
              executionId: "risk-attempt-2",
              stepId: "check-risk",
              status: "COMPLETED",
              attemptNumber: 2,
            },
            {
              executionId: "approval-attempt-1",
              stepId: "approve-loan",
              status: "COMPLETED",
              attemptNumber: 1,
            },
          ],
        },
      }),
    ),
    http.get(
      "/api/v1/process-instances/correlation-instance/step-executions",
      () =>
        HttpResponse.json({
          items: [
            {
              id: "approval-attempt-1",
              stepId: "approve-loan",
              stepType: "USER_TASK",
              attemptNumber: 1,
              status: "COMPLETED",
              startedAt: "2026-04-01T00:00:05.000Z",
              endedAt: "2026-04-01T00:00:06.000Z",
              hasFailure: false,
              hasInputSnapshot: false,
              hasOutputSnapshot: false,
            },
            {
              id: "risk-attempt-2",
              stepId: "check-risk",
              stepType: "SERVICE_TASK",
              attemptNumber: 2,
              status: "COMPLETED",
              startedAt: "2026-04-01T00:00:03.000Z",
              endedAt: "2026-04-01T00:00:04.000Z",
              hasFailure: false,
              hasInputSnapshot: true,
              hasOutputSnapshot: true,
            },
          ],
          nextCursor: null,
        }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "risk-attempt-2" });
  fireEvent.click(
    await screen.findByRole("button", { name: /Check Risk, COMPLETED/ }),
  );

  expect(
    screen.getByText("Highlighting Step Executions for Check Risk"),
  ).toBeVisible();
  expect(screen.getByRole("row", { name: /risk-attempt-2/ })).toHaveAttribute(
    "aria-current",
    "true",
  );
  expect(
    screen.getByRole("row", { name: /approval-attempt-1/ }),
  ).not.toHaveAttribute("aria-current");
  expect(
    screen.getByRole("cell", { name: "approval-attempt-1" }),
  ).toBeVisible();
});

it("pages through Step Execution attempts with opaque cursors", async () => {
  window.history.replaceState(null, "", "/process-instances/paged-history");
  server.use(
    http.get("/api/v1/process-instances/paged-history", () =>
      HttpResponse.json({
        instance: {
          id: "paged-history",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "COMPLETED",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [
            { id: "check-risk", name: "Check Risk", type: "SERVICE_TASK" },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get(
      "/api/v1/process-instances/paged-history/step-executions",
      ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        return HttpResponse.json(
          cursor
            ? {
                items: [
                  {
                    id: "older-attempt",
                    stepId: "check-risk",
                    stepType: "SERVICE_TASK",
                    attemptNumber: 1,
                    status: "FAILED",
                    startedAt: "2026-04-01T00:00:01.000Z",
                    endedAt: "2026-04-01T00:00:02.000Z",
                    hasFailure: true,
                    hasInputSnapshot: false,
                    hasOutputSnapshot: false,
                  },
                ],
                nextCursor: null,
              }
            : {
                items: [
                  {
                    id: "newer-attempt",
                    stepId: "check-risk",
                    stepType: "SERVICE_TASK",
                    attemptNumber: 2,
                    status: "COMPLETED",
                    startedAt: "2026-04-01T00:00:03.000Z",
                    endedAt: "2026-04-01T00:00:04.000Z",
                    hasFailure: false,
                    hasInputSnapshot: false,
                    hasOutputSnapshot: false,
                  },
                ],
                nextCursor: "older-attempt-cursor",
              },
        );
      },
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "newer-attempt" });
  fireEvent.click(
    screen.getByRole("button", { name: "Next Step Execution page" }),
  );
  expect(
    await screen.findByRole("cell", { name: "older-attempt" }),
  ).toBeVisible();

  fireEvent.click(
    screen.getByRole("button", { name: "Previous Step Execution page" }),
  );
  expect(
    await screen.findByRole("cell", { name: "newer-attempt" }),
  ).toBeVisible();
});

it("keeps cached Step Executions visible during a temporary failure", async () => {
  window.history.replaceState(null, "", "/process-instances/stale-history");
  let historyRequestCount = 0;
  server.use(
    http.get("/api/v1/process-instances/stale-history", () =>
      HttpResponse.json({
        instance: {
          id: "stale-history",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "WAITING",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get("/api/v1/process-instances/stale-history/step-executions", () => {
      historyRequestCount += 1;
      if (historyRequestCount > 1) {
        return new HttpResponse(null, { status: 503 });
      }
      return HttpResponse.json({
        items: [
          {
            id: "cached-attempt",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 1,
            status: "RUNNING",
            startedAt: "2026-04-01T00:00:01.000Z",
            endedAt: null,
            hasFailure: false,
            hasInputSnapshot: false,
            hasOutputSnapshot: false,
          },
        ],
        nextCursor: null,
      });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "cached-attempt" });
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh Step Executions" }),
  );
  await waitFor(() => expect(historyRequestCount).toBe(2));

  expect(screen.getByRole("cell", { name: "cached-attempt" })).toBeVisible();
  expect(screen.getByText("Stale Step Execution data")).toBeVisible();
});

it("shows loading and empty Step Execution states", async () => {
  window.history.replaceState(null, "", "/process-instances/empty-history");
  let releaseHistory: ((response: Response) => void) | undefined;
  server.use(
    http.get("/api/v1/process-instances/empty-history", () =>
      HttpResponse.json({
        instance: {
          id: "empty-history",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "ACTIVE",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get(
      "/api/v1/process-instances/empty-history/step-executions",
      () =>
        new Promise<Response>((resolve) => {
          releaseHistory = resolve;
        }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("heading", {
    name: "Process Instance empty-history",
  });
  expect(screen.getByText("Loading Step Executions…")).toBeVisible();

  act(() => {
    releaseHistory?.(
      HttpResponse.json({ items: [], nextCursor: null }) as Response,
    );
  });
  expect(await screen.findByText("No Step Executions recorded.")).toBeVisible();
});

it("fetches and renders typed Current Variables only while the Variables view is open", async () => {
  window.history.replaceState(null, "", "/process-instances/typed-variables");
  let currentVariableRequestCount = 0;
  server.use(
    http.get("/api/v1/process-instances/typed-variables", () =>
      HttpResponse.json({
        instance: {
          id: "typed-variables",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "WAITING",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get("/api/v1/process-instances/typed-variables/variables", () => {
      currentVariableRequestCount += 1;
      return HttpResponse.json({
        current: {
          status: "present",
          value: {
            applicant: "Ada",
            approved: false,
            score: 720,
            notes: null,
            profile: { country: "VN" },
            tags: ["priority", 3],
          },
          sizeBytes: 112,
        },
      });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("heading", {
    name: "Process Instance typed-variables",
  });
  expect(currentVariableRequestCount).toBe(0);

  fireEvent.click(screen.getByRole("tab", { name: "Variables" }));

  expect(
    await screen.findByRole("heading", { name: "Current Variables" }),
  ).toBeVisible();
  expect(currentVariableRequestCount).toBe(1);
  expect(
    await screen.findByRole("row", { name: 'applicant string "Ada"' }),
  ).toBeVisible();
  expect(
    screen.getByRole("row", { name: "approved boolean false" }),
  ).toBeVisible();
  expect(screen.getByRole("row", { name: "score number 720" })).toBeVisible();
  expect(screen.getByRole("row", { name: "notes null null" })).toBeVisible();
  expect(
    screen.getByRole("row", {
      name: 'profile object {"country":"VN"}',
    }),
  ).toBeVisible();
  expect(
    screen.getByRole("row", { name: 'tags array ["priority",3]' }),
  ).toBeVisible();
});

it("loads completed Variable Snapshots only when expanded and caches them", async () => {
  vi.useFakeTimers();
  window.history.replaceState(null, "", "/process-instances/lazy-snapshots");
  let snapshotRequestCount = 0;
  server.use(
    http.get("/api/v1/process-instances/lazy-snapshots", () =>
      HttpResponse.json({
        instance: {
          id: "lazy-snapshots",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "COMPLETED",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get("/api/v1/process-instances/lazy-snapshots/variables", () =>
      HttpResponse.json({
        current: {
          status: "present",
          value: {},
          sizeBytes: 2,
        },
      }),
    ),
    http.get("/api/v1/process-instances/lazy-snapshots/step-executions", () =>
      HttpResponse.json({
        items: [
          {
            id: "risk-attempt-1",
            stepId: "check-risk",
            stepType: "SERVICE_TASK",
            attemptNumber: 1,
            status: "COMPLETED",
            startedAt: "2026-05-01T00:00:01.000Z",
            endedAt: "2026-05-01T00:00:02.000Z",
            hasFailure: false,
            hasInputSnapshot: true,
            hasOutputSnapshot: false,
          },
        ],
        nextCursor: null,
      }),
    ),
    http.get(
      "/api/v1/process-instances/lazy-snapshots/step-executions/risk-attempt-1/variables",
      () => {
        snapshotRequestCount += 1;
        return HttpResponse.json({
          recordedInput: {
            status: "present",
            value: { applicant: "Ada", notes: null },
            sizeBytes: 32,
          },
          recordedOutput: {
            status: "notRecorded",
          },
        });
      },
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await vi.waitFor(() =>
    expect(
      screen.getByRole("heading", {
        name: "Process Instance lazy-snapshots",
      }),
    ).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("tab", { name: "Variables" }));
  await vi.waitFor(() =>
    expect(
      screen.getByRole("button", {
        name: "Expand snapshots for risk-attempt-1",
      }),
    ).toBeVisible(),
  );
  const expand = screen.getByRole("button", {
    name: "Expand snapshots for risk-attempt-1",
  });
  expect(snapshotRequestCount).toBe(0);

  fireEvent.click(expand);
  await vi.waitFor(() =>
    expect(
      screen.getByRole("row", { name: 'applicant string "Ada"' }),
    ).toBeVisible(),
  );
  expect(screen.getByRole("row", { name: "notes null null" })).toBeVisible();
  expect(screen.getByText("Recorded Output: Not recorded")).toBeVisible();
  expect(snapshotRequestCount).toBe(1);

  fireEvent.click(
    screen.getByRole("button", {
      name: "Collapse snapshots for risk-attempt-1",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "Expand snapshots for risk-attempt-1",
    }),
  );
  expect(
    screen.getByRole("row", { name: 'applicant string "Ada"' }),
  ).toBeVisible();
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(snapshotRequestCount).toBe(1);
});

it("polls Current Variables only while the Variables view is visible", async () => {
  vi.useFakeTimers();
  window.history.replaceState(null, "", "/process-instances/polled-variables");
  let currentVariableRequestCount = 0;
  server.use(
    http.get("/api/v1/process-instances/polled-variables", () =>
      HttpResponse.json({
        instance: {
          id: "polled-variables",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "ACTIVE",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get("/api/v1/process-instances/polled-variables/variables", () => {
      currentVariableRequestCount += 1;
      return HttpResponse.json({
        current: {
          status: "present",
          value: {},
          sizeBytes: 2,
        },
      });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await vi.waitFor(() =>
    expect(
      screen.getByRole("heading", {
        name: "Process Instance polled-variables",
      }),
    ).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("tab", { name: "Variables" }));
  await vi.waitFor(() => expect(currentVariableRequestCount).toBe(1));

  await act(() => vi.advanceTimersByTimeAsync(10_000));
  await vi.waitFor(() => expect(currentVariableRequestCount).toBe(3));

  fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(currentVariableRequestCount).toBe(3);
});

it("shows absent and oversized documents without variable mutation affordances", async () => {
  window.history.replaceState(null, "", "/process-instances/bounded-variables");
  server.use(
    http.get("/api/v1/process-instances/bounded-variables", () =>
      HttpResponse.json({
        instance: {
          id: "bounded-variables",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "COMPLETED",
          currentStepIds: [],
          businessKey: null,
        },
        definition: {
          id: "loan-approval",
          version: 1,
          name: "Loan Approval",
          steps: [],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: null,
          latestByStep: [],
        },
      }),
    ),
    http.get("/api/v1/process-instances/bounded-variables/variables", () =>
      HttpResponse.json({
        current: {
          status: "contentTooLarge",
          sizeBytes: 6_000_000,
        },
      }),
    ),
    http.get(
      "/api/v1/process-instances/bounded-variables/step-executions",
      () =>
        HttpResponse.json({
          items: [
            {
              id: "bounded-execution",
              stepId: "archive",
              stepType: "SERVICE_TASK",
              attemptNumber: 1,
              status: "COMPLETED",
              startedAt: "2026-05-01T00:00:01.000Z",
              endedAt: "2026-05-01T00:00:02.000Z",
              hasFailure: false,
              hasInputSnapshot: false,
              hasOutputSnapshot: true,
            },
          ],
          nextCursor: null,
        }),
    ),
    http.get(
      "/api/v1/process-instances/bounded-variables/step-executions/bounded-execution/variables",
      () =>
        HttpResponse.json({
          recordedInput: { status: "notRecorded" },
          recordedOutput: {
            status: "contentTooLarge",
            sizeBytes: 6_100_000,
          },
        }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstanceTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("heading", {
    name: "Process Instance bounded-variables",
  });
  fireEvent.click(screen.getByRole("tab", { name: "Variables" }));

  expect(
    await screen.findByText(
      "Current Variables content is too large (6000000 bytes).",
    ),
  ).toBeVisible();
  expect(
    screen.getByText(
      "Snapshots are recorded execution boundaries, not a complete variable-change history.",
    ),
  ).toBeVisible();

  fireEvent.click(
    await screen.findByRole("button", {
      name: "Expand snapshots for bounded-execution",
    }),
  );
  expect(await screen.findByText("Recorded Input: Not recorded")).toBeVisible();
  expect(
    screen.getByText("Recorded Output: Content too large (6100000 bytes)."),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", {
      name: /edit|change type|download|export/i,
    }),
  ).not.toBeInTheDocument();
});
