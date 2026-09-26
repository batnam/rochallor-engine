import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { OverviewRoute } from "../src/routes/OverviewRoute";
import { RouteHarness } from "./route-harness";

const server = setupServer();
const clients: QueryClient[] = [];
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterAll(() => server.close());
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  server.resetHandlers();
  vi.useRealTimers();
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
});

function start(fail = false) {
  const state = { fail, requests: 0, ages: [] as string[] };
  server.use(
    http.get("/api/v1/overview", () => {
      state.requests++;
      return state.fail
        ? new HttpResponse(null, { status: 503 })
        : HttpResponse.json({
            observedAt: "2026-01-02T00:00:00Z",
            scope: "All instances",
            nextCursor: null,
            items: [
              {
                definitionId: "flow",
                definitionVersion: 2,
                name: "Flow",
                active: 2,
                waiting: 1,
                failed: 0,
              },
            ],
          });
    }),
    http.get("/api/v1/overview/steps", ({ request }) => {
      state.ages.push(
        new URL(request.url).searchParams.get("minStepAgeSeconds") ?? "",
      );
      return HttpResponse.json({
        observedAt: "2026-01-02T00:00:00Z",
        stepStartedBefore: "2026-01-01T23:30:00.123Z",
        minStepAgeSeconds: 1800,
        scope: "Current steps",
        nextCursor: null,
        items: [{ stepId: "work", instances: 2, aged: 1, ageUnavailable: 0 }],
      });
    }),
  );
  renderOverview();
  return state;
}

function renderOverview(url = "/overview") {
  window.history.replaceState(null, "", url);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <RouteHarness>
        {(location, navigation) => (
          <OverviewRoute search={location.search} navigation={navigation} />
        )}
      </RouteHarness>
    </QueryClientProvider>,
  );
}

it("drills down with the exact version, current step and observed cutoff", async () => {
  const state = start();
  fireEvent.click(
    await screen.findByRole("button", { name: "Inspect flow v2" }),
  );
  const row = await screen.findByRole("row", { name: "work 2 1 0" });
  const target = new URL(
    within(row).getAllByRole("link")[1].getAttribute("href") ?? "",
    "http://localhost",
  );
  expect(target.searchParams.get("definitionVersion")).toBe("2");
  expect(target.searchParams.get("currentStepId")).toBe("work");
  expect(target.searchParams.get("stepStartedBefore")).toBe(
    "2026-01-01T23:30:00.123Z",
  );
  expect(target.searchParams.getAll("status")).toEqual(["ACTIVE", "WAITING"]);
  fireEvent.change(screen.getByLabelText("Minimum execution age (seconds)"), {
    target: { value: "60" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply age threshold" }));
  await vi.waitFor(() => expect(state.ages).toContain("60"));
  expect(window.location.search).toContain("minStepAgeSeconds=60");
});

it("retains counts after refresh errors and recovers without reporting a healthy zero", async () => {
  const state = start();
  await screen.findByRole("button", { name: "Inspect flow v2" });
  state.fail = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh Overview" }));
  await screen.findByText(/Stale workflow counts/);
  expect(screen.getByRole("button", { name: "Inspect flow v2" })).toBeVisible();
  expect(screen.queryByText("No retained instances.")).not.toBeInTheDocument();
  state.fail = false;
  fireEvent.click(screen.getByRole("button", { name: "Refresh Overview" }));
  await vi.waitFor(() =>
    expect(screen.queryByText(/Stale workflow counts/)).not.toBeInTheDocument(),
  );
});

it("shows an initial error separately from empty counts", async () => {
  start(true);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Unable to load Overview",
  );
  expect(screen.queryByText("No retained instances.")).not.toBeInTheDocument();
});

it("polls every 15 seconds while visible and pauses background requests", async () => {
  vi.useFakeTimers();
  const state = start();
  await vi.waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Inspect flow v2" }),
    ).toBeVisible(),
  );
  await act(() => vi.advanceTimersByTimeAsync(15000));
  await vi.waitFor(() => expect(state.requests).toBe(2));
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "hidden",
  });
  act(() => window.dispatchEvent(new Event("visibilitychange")));
  await act(() => vi.advanceTimersByTimeAsync(30000));
  expect(state.requests).toBe(2);
});

it("searches via the API, resets pagination and selection, and preserves the filter across pages and history", async () => {
  const requests: URLSearchParams[] = [];
  server.use(
    http.get("/api/v1/overview", ({ request }) => {
      const parameters = new URL(request.url).searchParams;
      requests.push(parameters);
      return HttpResponse.json({
        observedAt: "2026-01-02T00:00:00Z",
        scope: "All instances",
        nextCursor: parameters.has("cursor") ? null : "next-workflow",
        items: [
          {
            definitionId: "checkout",
            definitionVersion: 1,
            name: "Shopping cart",
            active: 1,
            waiting: 0,
            failed: 0,
          },
        ],
      });
    }),
    http.get("/api/v1/overview/steps", () =>
      HttpResponse.json({ items: [], observedAt: "2026-01-02T00:00:00Z" }),
    ),
  );
  renderOverview(
    "/overview?workflow=old&cursor=old-page&definitionId=old&definitionVersion=2&stepCursor=old-step",
  );
  await screen.findByRole("button", { name: "Inspect checkout v1" });
  const input = screen.getByRole("searchbox", { name: "Search workflows" });
  expect(input).toHaveValue("old");
  fireEvent.change(input, { target: { value: "  Shopping cart  " } });
  expect(requests).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await vi.waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[1].toString()).toBe("workflow=Shopping+cart");
  expect(window.location.search).toBe("?workflow=Shopping+cart");
  expect(input).toHaveValue("Shopping cart");
  expect(
    screen.queryByLabelText("Current step counts"),
  ).not.toBeInTheDocument();
  fireEvent.click(
    await screen.findByRole("button", { name: "Inspect checkout v1" }),
  );
  expect(new URLSearchParams(window.location.search).get("workflow")).toBe(
    "Shopping cart",
  );
  fireEvent.click(screen.getByRole("button", { name: "Next workflow page" }));
  await vi.waitFor(() => expect(requests).toHaveLength(3));
  expect(requests[2].get("workflow")).toBe("Shopping cart");
  expect(requests[2].get("cursor")).toBe("next-workflow");
  await screen.findByRole("button", { name: "Inspect checkout v1" });
  fireEvent.click(screen.getByRole("button", { name: "First workflow page" }));
  await vi.waitFor(() => expect(requests).toHaveLength(4));
  expect(requests[3].toString()).toBe("workflow=Shopping+cart");
  fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
  await vi.waitFor(() => expect(requests).toHaveLength(5));
  expect(requests[4].toString()).toBe("");
  expect(input).toHaveValue("");
  expect(new URLSearchParams(window.location.search).has("definitionId")).toBe(
    false,
  );
  act(() => window.history.back());
  await vi.waitFor(() => expect(input).toHaveValue("Shopping cart"));
  expect(new URLSearchParams(window.location.search).get("workflow")).toBe(
    "Shopping cart",
  );
  act(() => window.history.forward());
  await vi.waitFor(() => expect(input).toHaveValue(""));
});

it("shows an empty search result separately from a failed search and allows clearing it", async () => {
  server.use(
    http.get("/api/v1/overview", ({ request }) =>
      new URL(request.url).searchParams.get("workflow") === "unavailable"
        ? new HttpResponse(null, { status: 503 })
        : HttpResponse.json({
            items: [],
            nextCursor: null,
            observedAt: "2026-01-02T00:00:00Z",
          }),
    ),
  );
  renderOverview("/overview?workflow=missing");
  await screen.findByText("No workflows match your search.");
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "unavailable" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await screen.findByRole("alert");
  expect(
    screen.queryByText("No workflows match your search."),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
  await screen.findByText("No retained instances.");
  expect(screen.getByRole("searchbox")).toHaveValue("");
});
