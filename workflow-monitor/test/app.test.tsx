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
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";

import { App } from "../src/App";

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

it("navigates from the Monitor home page to Incidents", async () => {
  server.use(
    http.get("/api/v1/incidents", () =>
      HttpResponse.json({ items: [], nextCursor: null }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  fireEvent.click(await screen.findByRole("link", { name: "Incidents" }));

  expect(
    await screen.findByRole("heading", { name: "Incidents" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/incidents");
});

it("navigates from Incident detail to the failed diagram step", async () => {
  window.history.replaceState(null, "", "/incidents/execution-service");
  server.use(
    http.get("/api/v1/incidents/execution-service", () =>
      HttpResponse.json({
        incident: {
          id: "execution-service",
          processInstanceId: "instance-service",
          definitionId: "loan-approval",
          definitionVersion: 1,
          definitionName: "Loan Approval",
          stepId: "charge-card",
          stepType: "SERVICE_TASK",
          attemptNumber: 2,
          occurredAt: "2026-03-01T00:03:00.000Z",
          errorDetails: "card processor unavailable",
          processInstance: {
            id: "instance-service",
            status: "FAILED",
            businessKey: "loan-001",
          },
          job: null,
        },
      }),
    ),
    http.get("/api/v1/process-instances/instance-service", () =>
      HttpResponse.json({
        instance: {
          id: "instance-service",
          definitionId: "loan-approval",
          definitionVersion: 1,
          status: "FAILED",
          businessKey: "loan-001",
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
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  fireEvent.click(
    await screen.findByRole("link", {
      name: "Process Instance instance-service",
    }),
  );

  expect(
    await screen.findByRole("heading", {
      name: "Process Instance instance-service",
    }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/process-instances/instance-service");
  expect(window.location.search).toBe("?stepId=charge-card");
});

it("shows Not Found for an unknown route", () => {
  window.history.replaceState(null, "", "/unknown");
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  expect(screen.getByRole("heading", { name: "Not Found" })).toBeVisible();
});

it("restores the matching view through browser Back and Forward", async () => {
  window.history.replaceState(null, "", "/?businessKey=first");
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "instance-failed" });
  fireEvent.change(screen.getByLabelText("Business Key"), {
    target: { value: "second" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply Filters" }));
  expect(window.location.search).toBe("?businessKey=second");

  window.history.back();
  await waitFor(() =>
    expect(window.location.search).toBe("?businessKey=first"),
  );
  expect(screen.getByLabelText("Business Key")).toHaveValue("first");

  window.history.forward();
  await waitFor(() =>
    expect(window.location.search).toBe("?businessKey=second"),
  );
  expect(screen.getByLabelText("Business Key")).toHaveValue("second");
});

it("navigates from the list to Process Instance metadata", async () => {
  server.use(
    http.get("/api/v1/process-instances/instance-failed", () =>
      HttpResponse.json({
        instance: {
          id: "instance-failed",
          definitionId: "loan-approval",
          definitionVersion: 2,
          status: "FAILED",
          currentStepIds: [],
          startedAt: "2026-01-02T00:00:00.000Z",
          completedAt: "2026-01-02T00:05:00.000Z",
          failureReason: "worker failed",
          businessKey: "loan-002",
        },
        definition: {
          id: "loan-approval",
          version: 2,
          name: "Loan Approval",
          steps: [
            { id: "check-risk", name: "Check Risk", type: "SERVICE_TASK" },
          ],
        },
        executionOverlay: {
          currentTokenStepIds: [],
          failedStepId: "check-risk",
          latestByStep: [],
        },
      }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );

  fireEvent.click(await screen.findByRole("link", { name: "instance-failed" }));

  expect(
    await screen.findByRole("heading", {
      name: "Process Instance instance-failed",
    }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/process-instances/instance-failed");
});
