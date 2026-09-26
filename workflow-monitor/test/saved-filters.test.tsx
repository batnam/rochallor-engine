import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SavedFilters } from "../src/SavedFilters";
import {
  loadSavedFilters,
  normalizeFilterSearch,
  savedFilterKey,
  storeSavedFilters,
} from "../src/saved-filter-storage";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});

it("round-trips every instance filter while resetting pagination and preserving the absolute cutoff", () => {
  const search =
    "?definitionId=flow&definitionVersion=02&currentStepId=review&stepStartedBefore=2026-05-01T00%3A00%3A00.123Z&businessKey=case%2F42&status=WAITING&status=ACTIVE&status=ACTIVE&from=2026-01-01T00%3A00%3A00Z&to=2026-06-01T00%3A00%3A00Z&cursor=opaque&pageSize=20";
  const stored = storeSavedFilters("instances", [
    { id: "1", name: " Review ", search },
  ]);
  expect(loadSavedFilters("instances")).toEqual(stored);
  expect(stored[0].name).toBe("Review");
  const params = new URLSearchParams(stored[0].search);
  expect(Object.fromEntries(params)).toEqual({
    definitionId: "flow",
    definitionVersion: "2",
    currentStepId: "review",
    stepStartedBefore: "2026-05-01T00:00:00.123Z",
    businessKey: "case/42",
    status: "WAITING",
    from: "2026-01-01T00:00:00Z",
    to: "2026-06-01T00:00:00Z",
  });
  expect(params.getAll("status")).toEqual(["ACTIVE", "WAITING"]);
  expect(loadSavedFilters("incidents")).toEqual([]);
  expect(
    JSON.parse(localStorage.getItem(savedFilterKey("instances")) ?? ""),
  ).toEqual({ version: 1, views: stored });
});

it.each([
  "variables=secret",
  "status=UNKNOWN",
  "businessKey=a&businessKey=b",
  "definitionVersion=2",
  "definitionId=flow&definitionVersion=0",
  "currentStepId=review",
  "definitionId=flow&stepStartedBefore=2026-01-01T00:00:00Z",
  "definitionId=flow&currentStepId=review&stepStartedBefore=tomorrow",
  "from=tomorrow",
  "from=2026-02-01T00:00:00Z&to=2026-01-01T00:00:00Z",
])("rejects invalid or stale filter definitions: %s", (search) => {
  expect(() => normalizeFilterSearch("instances", search)).toThrow();
});

it("validates Incident fields independently", () => {
  expect(
    normalizeFilterSearch(
      "incidents",
      "jobType=risk&definitionId=flow&cursor=x",
    ),
  ).toBe("definitionId=flow&jobType=risk");
  expect(() => normalizeFilterSearch("incidents", "status=ACTIVE")).toThrow();
});

it("bounds names, filter count, URL length, and stored document size", () => {
  const view = { id: "1", name: "Review", search: "" };
  expect(() =>
    storeSavedFilters("instances", [
      view,
      { ...view, id: "2", name: " review " },
    ]),
  ).toThrow(/unique names/);
  expect(() =>
    storeSavedFilters("instances", [{ ...view, name: " " }]),
  ).toThrow();
  expect(() =>
    storeSavedFilters("instances", [{ ...view, name: "x".repeat(81) }]),
  ).toThrow();
  expect(() =>
    storeSavedFilters(
      "instances",
      Array.from({ length: 21 }, (_, i) => ({
        id: String(i),
        name: String(i),
        search: "",
      })),
    ),
  ).toThrow(/at most 20/);
  expect(() =>
    normalizeFilterSearch("instances", `businessKey=${"x".repeat(4096)}`),
  ).toThrow(/4,096/);
  expect(() =>
    storeSavedFilters(
      "instances",
      Array.from({ length: 20 }, (_, i) => ({
        id: String(i),
        name: String(i),
        search: `businessKey=${"x".repeat(4000)}`,
      })),
    ),
  ).toThrow(/64 KiB/);
  expect(localStorage.getItem(savedFilterKey("instances"))).toBeNull();
});

it.each([
  "{",
  "null",
  '{"version":2,"views":[]}',
  '{"version":1,"views":{}}',
  "x".repeat(65537),
])("rejects corrupt or incompatible storage without overwriting it", (raw) => {
  localStorage.setItem(savedFilterKey("instances"), raw);
  expect(() => loadSavedFilters("instances")).toThrow();
  expect(localStorage.getItem(savedFilterKey("instances"))).toBe(raw);
});

it("saves, reloads, applies, renames, and deletes a filter without persisting results", () => {
  const navigation = { push: vi.fn(), back: vi.fn() };
  const first = render(
    <SavedFilters
      kind="instances"
      search="?businessKey=loan-42&cursor=old"
      navigation={navigation}
    />,
  );
  fireEvent.change(screen.getByLabelText("Filter name"), {
    target: { value: "Loan" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save applied filters" }));
  first.unmount();
  render(
    <SavedFilters
      kind="instances"
      search="?status=FAILED"
      navigation={navigation}
    />,
  );
  const saved = loadSavedFilters("instances")[0];
  fireEvent.change(screen.getByLabelText("Saved filter", { exact: true }), {
    target: { value: saved.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply saved filter" }));
  expect(navigation.push).toHaveBeenCalledWith("/?businessKey=loan-42");
  fireEvent.change(screen.getByLabelText("Filter name"), {
    target: { value: "Loan review" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Rename saved filter" }));
  expect(loadSavedFilters("instances")[0].name).toBe("Loan review");
  expect(Object.keys(loadSavedFilters("instances")[0])).toEqual([
    "id",
    "name",
    "search",
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Delete saved filter" }));
  expect(loadSavedFilters("instances")).toEqual([]);
  expect(
    screen.getByRole("button", { name: "Apply saved filter" }),
  ).toBeDisabled();
});

it("recovers corrupt preferences through explicit clear and keeps list kinds separate", () => {
  localStorage.setItem(savedFilterKey("incidents"), "broken");
  storeSavedFilters("instances", [{ id: "1", name: "All", search: "" }]);
  render(
    <SavedFilters
      kind="incidents"
      search="?jobType=risk"
      navigation={{ push: vi.fn(), back: vi.fn() }}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Ordinary searches remain available",
  );
  fireEvent.click(screen.getByRole("button", { name: "Clear saved filters" }));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(loadSavedFilters("instances")).toHaveLength(1);
  expect(loadSavedFilters("incidents")).toEqual([]);
});

it("restores the selected saved search after the list remounts for its new URL", () => {
  storeSavedFilters("instances", [
    { id: "selected", name: "Loan", search: "businessKey=loan-42" },
  ]);
  render(
    <SavedFilters
      kind="instances"
      search="?businessKey=loan-42&cursor=old"
      navigation={{ push: vi.fn(), back: vi.fn() }}
    />,
  );
  expect(screen.getByLabelText("Saved filter", { exact: true })).toHaveValue(
    "selected",
  );
  expect(screen.getByLabelText("Filter name")).toHaveValue("Loan");
  expect(
    screen.getByRole("button", { name: "Rename saved filter" }),
  ).toBeEnabled();
});

it("handles denied storage reads and quota errors without a render failure", () => {
  const read = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("Storage denied");
  });
  render(
    <SavedFilters
      kind="instances"
      search=""
      navigation={{ push: vi.fn(), back: vi.fn() }}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent("Storage denied");
  read.mockRestore();
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("Quota exceeded");
  });
  fireEvent.change(screen.getByLabelText("Filter name"), {
    target: { value: "All" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save applied filters" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Quota exceeded");
  expect(screen.getByLabelText("Saved filter", { exact: true })).toHaveValue(
    "",
  );
});
