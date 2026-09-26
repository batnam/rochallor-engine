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

import { ProcessInstancesRoute } from "../src/routes/ProcessInstancesRoute";
import { RouteHarness } from "./route-harness";

function ProcessInstancesTestRoute(): ReactNode {
  return (
    <RouteHarness>
      {(location, navigation) => (
        <ProcessInstancesRoute
          navigation={navigation}
          search={location.search}
        />
      )}
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

it("trims and encodes opaque IDs for direct lookup and rejects an empty ID", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );
  await screen.findByRole("cell", { name: "instance-active" });
  fireEvent.click(screen.getByRole("button", { name: "Open by Instance ID" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Enter an Instance ID");
  fireEvent.change(screen.getByLabelText("Instance ID", { exact: true }), {
    target: { value: "  id/with ?#  " },
  });
  fireEvent.click(screen.getByRole("button", { name: "Open by Instance ID" }));
  expect(window.location.pathname).toBe(
    "/process-instances/id%2Fwith%20%3F%23",
  );
  client.clear();
});

it("shows Process Instances returned by the BFF", async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByRole("cell", { name: "instance-failed" }),
  ).toBeVisible();
  expect(
    screen.getByRole("columnheader", { name: "Definition ID" }),
  ).toBeVisible();
  expect(screen.getAllByRole("cell", { name: "loan-approval" })).toHaveLength(
    2,
  );
  expect(screen.getByRole("cell", { name: "FAILED" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "instance-active" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "ACTIVE" })).toBeVisible();
  expect(screen.getByRole("cell", { name: "loan-002" })).toBeVisible();
  expect(
    screen.getByRole("cell", { name: "2026-01-02T00:00:00.000Z" }),
  ).toBeVisible();
  expect(screen.getByRole("cell", { name: "0h 5m 0s" })).toBeVisible();
  expect(screen.getByText(/Instances updated/)).toBeVisible();
});

it("keeps the filters editable and explains an invalid time range before sending it", async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );
  await screen.findByRole("cell", { name: "instance-active" });
  fireEvent.change(screen.getByLabelText("Started From (UTC)"), {
    target: { value: "2026-02-01T00:00" },
  });
  fireEvent.change(screen.getByLabelText("Started To (UTC)"), {
    target: { value: "2026-01-01T00:00" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply Filters" }));
  expect(screen.getByRole("alert")).toHaveTextContent(
    "From must be earlier than To (UTC).",
  );
  expect(window.location.search).toBe("");
  expect(screen.getByRole("cell", { name: "instance-active" })).toBeVisible();
});

it("shows API filter validation beside the form and can recover without reloading", async () => {
  window.history.replaceState(null, "", "/?from=bad-date");
  server.use(
    http.get("/api/v1/process-instances", ({ request }) =>
      new URL(request.url).searchParams.get("from") === "bad-date"
        ? HttpResponse.json(
            { message: "Invalid UTC timestamp" },
            { status: 400 },
          )
        : HttpResponse.json({ items: [], nextCursor: null }),
    ),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Invalid UTC timestamp",
  );
  expect(
    screen.queryByText(/Check the Monitor API connection/),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Started From (UTC)"), {
    target: { value: "2026-01-01T00:00" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply Filters" }));
  await screen.findByText("No Process Instances found");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("loads Workflow Definition options and writes applied filters to the URL and request", async () => {
  let filteredRequest: URL | undefined;
  server.use(
    http.get("/api/v1/process-instances", ({ request }) => {
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
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("option", { name: "Loan Approval" });
  fireEvent.change(screen.getByLabelText("Workflow Definition"), {
    target: { value: "loan-approval" },
  });
  fireEvent.click(screen.getByLabelText("ACTIVE"));
  fireEvent.click(screen.getByLabelText("FAILED"));
  fireEvent.change(screen.getByLabelText("Business Key"), {
    target: { value: "loan-002" },
  });
  fireEvent.change(screen.getByLabelText("Started From (UTC)"), {
    target: { value: "2026-01-01T00:00:00" },
  });
  fireEvent.change(screen.getByLabelText("Started To (UTC)"), {
    target: { value: "2026-02-01T00:00:00" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply Filters" }));

  await waitFor(() => expect(filteredRequest).toBeDefined());
  expect(filteredRequest?.searchParams.get("definitionId")).toBe(
    "loan-approval",
  );
  expect(filteredRequest?.searchParams.getAll("status")).toEqual([
    "ACTIVE",
    "FAILED",
  ]);
  expect(filteredRequest?.searchParams.get("businessKey")).toBe("loan-002");
  expect(filteredRequest?.searchParams.get("from")).toBe(
    "2026-01-01T00:00:00Z",
  );
  expect(filteredRequest?.searchParams.get("to")).toBe("2026-02-01T00:00:00Z");
  expect(window.location.search).toBe(filteredRequest?.search);
});

it("restores filters from a shared URL", async () => {
  window.history.replaceState(
    null,
    "",
    "/?definitionId=loan-approval&status=WAITING&businessKey=loan-009&from=2026-01-01T00%3A00%3A00Z&to=2026-02-01T00%3A00%3A00Z",
  );
  let requestedUrl: URL | undefined;
  server.use(
    http.get("/api/v1/process-instances", ({ request }) => {
      requestedUrl = new URL(request.url);
      return HttpResponse.json({ items: [], nextCursor: null });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("option", { name: "Loan Approval" });
  expect(screen.getByLabelText("Workflow Definition")).toHaveValue(
    "loan-approval",
  );
  expect(screen.getByLabelText("WAITING")).toBeChecked();
  expect(screen.getByLabelText("Business Key")).toHaveValue("loan-009");
  expect(screen.getByLabelText("Started From (UTC)")).toHaveValue(
    "2026-01-01T00:00",
  );
  expect(screen.getByLabelText("Started To (UTC)")).toHaveValue(
    "2026-02-01T00:00",
  );
  expect(requestedUrl?.search).toBe(window.location.search);
});

it("navigates with Newest, Previous, and Next without page numbers", async () => {
  server.use(
    http.get("/api/v1/process-instances", ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("cursor");
      return HttpResponse.json(
        cursor
          ? {
              items: [{ id: "older-instance", status: "COMPLETED" }],
              nextCursor: null,
            }
          : {
              items: [{ id: "newer-instance", status: "ACTIVE" }],
              nextCursor: "cursor-for-older-page",
            },
      );
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "newer-instance" });
  expect(screen.queryByText(/Page \d/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Next" }));

  expect(
    await screen.findByRole("cell", { name: "older-instance" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("cursor")).toBe(
    "cursor-for-older-page",
  );

  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  expect(
    await screen.findByRole("cell", { name: "newer-instance" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).has("cursor")).toBe(false);

  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("cell", { name: "older-instance" });
  fireEvent.click(screen.getByRole("button", { name: "Newest" }));
  expect(
    await screen.findByRole("cell", { name: "newer-instance" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).has("cursor")).toBe(false);
});

it("polls every five seconds only while visible and supports manual refresh", async () => {
  vi.useFakeTimers();
  let requestCount = 0;
  server.use(
    http.get("/api/v1/process-instances", () => {
      requestCount += 1;
      return HttpResponse.json({ items: [], nextCursor: null });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  await vi.waitFor(() => expect(requestCount).toBe(1));
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await vi.waitFor(() => expect(requestCount).toBe(2));

  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "hidden",
  });
  document.dispatchEvent(new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(requestCount).toBe(2);

  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
  document.dispatchEvent(new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await vi.waitFor(() => expect(requestCount).toBe(3));
  const countBeforeManualRefresh = requestCount;

  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await vi.waitFor(() =>
    expect(requestCount).toBe(countBeforeManualRefresh + 1),
  );
});

it("keeps cached rows visible and marks them stale after a temporary failure", async () => {
  let requestCount = 0;
  server.use(
    http.get("/api/v1/process-instances", () => {
      requestCount += 1;
      if (requestCount > 1) {
        return new HttpResponse(null, { status: 503 });
      }
      return HttpResponse.json({
        items: [{ id: "cached-instance", status: "WAITING" }],
        nextCursor: null,
      });
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ProcessInstancesTestRoute />
    </QueryClientProvider>,
  );

  await screen.findByRole("cell", { name: "cached-instance" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(requestCount).toBe(2));

  expect(screen.getByRole("cell", { name: "cached-instance" })).toBeVisible();
  expect(screen.getByText("Stale data")).toBeVisible();
});
