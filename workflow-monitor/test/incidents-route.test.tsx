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
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";

import { IncidentsRoute } from "../src/routes/IncidentsRoute";
import { RouteHarness } from "./route-harness";

function IncidentsTestRoute(): ReactNode {
  return (
    <RouteHarness>
      {(location, navigation) => {
        const match = /^\/incidents\/([^/]+)$/.exec(location.pathname);
        return (
          <IncidentsRoute
            incidentId={match ? decodeURIComponent(match[1]) : null}
            navigation={navigation}
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

it("shows canonical Incidents returned by the BFF", async () => {
  window.history.replaceState(null, "", "/incidents");
  server.use(
    http.get("/api/v1/incidents", () =>
      HttpResponse.json({
        items: [
          {
            id: "execution-service",
            processInstanceId: "instance-service",
            definitionId: "loan-approval",
            definitionVersion: 1,
            definitionName: "Loan Approval",
            stepId: "charge-card",
            stepType: "SERVICE_TASK",
            attemptNumber: 2,
            occurredAt: "2026-03-01T00:03:00.000Z",
            job: {
              id: "job-service",
              type: "payments",
              status: "FAILED",
            },
          },
          {
            id: "execution-script",
            processInstanceId: "instance-script",
            definitionId: "account-review",
            definitionVersion: 2,
            definitionName: "Account Review",
            stepId: "validate-account",
            stepType: "SCRIPT_TASK",
            attemptNumber: 1,
            occurredAt: "2026-03-01T00:02:00.000Z",
            job: null,
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
      <IncidentsTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("heading", { name: "Incidents" }),
  ).toBeVisible();
  expect(screen.getByRole("link", { name: "execution-service" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "Loan Approval v1" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "charge-card" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "payments" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "Not applicable" })).toBeVisible();
});

it("applies Incident definition, job type, and occurrence filters", async () => {
  window.history.replaceState(null, "", "/incidents");
  let filteredRequest: URL | undefined;
  server.use(
    http.get("/api/v1/incidents", ({ request }) => {
      const url = new URL(request.url);
      if (url.search) {
        filteredRequest = url;
      }
      return HttpResponse.json({ items: [], nextCursor: null });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <IncidentsTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("option", { name: "Loan Approval" });
  fireEvent.change(screen.getByLabelText("Workflow Definition"), {
    target: { value: "loan-approval" },
  });
  fireEvent.change(screen.getByLabelText("Job Type"), {
    target: { value: "payments" },
  });
  fireEvent.change(screen.getByLabelText("Occurred From (UTC)"), {
    target: { value: "2026-03-01T00:00:00Z" },
  });
  fireEvent.change(screen.getByLabelText("Occurred To (UTC)"), {
    target: { value: "2026-03-02T00:00:00Z" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Apply Incident Filters" }),
  );

  await waitFor(() => expect(filteredRequest).toBeDefined());
  expect(filteredRequest?.search).toBe(
    "?definitionId=loan-approval&jobType=payments&from=2026-03-01T00%3A00%3A00Z&to=2026-03-02T00%3A00%3A00Z",
  );
  expect(window.location.search).toBe(filteredRequest?.search);
});

it("continues the Incident list with the returned opaque cursor", async () => {
  window.history.replaceState(null, "", "/incidents");
  let cursorRequest: URL | undefined;
  server.use(
    http.get("/api/v1/incidents", ({ request }) => {
      const url = new URL(request.url);
      if (url.searchParams.has("cursor")) {
        cursorRequest = url;
        return HttpResponse.json({ items: [], nextCursor: null });
      }
      return HttpResponse.json({ items: [], nextCursor: "incident-cursor-2" });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <IncidentsTestRoute />
    </QueryClientProvider>,
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Next Incident page" }),
  );

  await waitFor(() => expect(cursorRequest).toBeDefined());
  expect(cursorRequest?.searchParams.get("cursor")).toBe("incident-cursor-2");
  expect(window.location.search).toBe("?cursor=incident-cursor-2");
});

it("shows Incident Error Details and affected Process Instance context", async () => {
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
          job: {
            id: "job-service",
            type: "payments",
            status: "FAILED",
          },
        },
      }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <IncidentsTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("heading", {
      name: "Incident execution-service",
    }),
  ).toBeVisible();
  expect(screen.getByRole("heading", { name: "Error Details" })).toBeVisible();
  expect(screen.getByText("card processor unavailable")).toBeVisible();
  expect(screen.getByText("payments")).toBeVisible();
  expect(
    screen.getByRole("link", {
      name: "Process Instance instance-service",
    }),
  ).toHaveAttribute(
    "href",
    "/process-instances/instance-service?stepId=charge-card",
  );
});

it("does not load Workflow Definition options for Incident detail", async () => {
  window.history.replaceState(null, "", "/incidents/execution-service");
  let workflowDefinitionRequests = 0;
  server.use(
    http.get("/api/v1/workflow-definitions", () => {
      workflowDefinitionRequests += 1;
      return HttpResponse.json({ items: [] });
    }),
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
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <IncidentsTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("heading", {
      name: "Incident execution-service",
    }),
  ).toBeVisible();
  expect(workflowDefinitionRequests).toBe(0);
});
