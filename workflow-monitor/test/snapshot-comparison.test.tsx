import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import {
  SnapshotComparison,
  compareSnapshots,
} from "../src/process-variables/SnapshotComparison";
import type { VariableDocument } from "../src/process-variables/VariableSnapshotInspector";

afterEach(cleanup);
const document = (value: unknown): VariableDocument => ({
  status: "present",
  value,
  sizeBytes: JSON.stringify(value).length,
});

it("compares returned delta keys without inventing removals or a complete post-step state", () => {
  const result = compareSnapshots(
    document({ a: 1, b: 2 }),
    document({ a: 3 }),
    "variableDelta",
  );
  expect(result.changes).toEqual([
    { key: "a", before: 1, after: 3, kind: "changed" },
  ]);
});

it("preserves missing, null, false, zero and type changes", () => {
  const result = compareSnapshots(
    document({ removed: null, typed: 0, unchanged: false }),
    document({ added: null, typed: "0", unchanged: false }),
    "fullState",
  );
  expect(result.changes).toEqual([
    { key: "added", before: undefined, after: null, kind: "added" },
    { key: "removed", before: null, after: undefined, kind: "removed" },
    { key: "typed", before: 0, after: "0", kind: "changed" },
  ]);
});

it("compares nested objects independent of key order and arrays in order", () => {
  expect(
    compareSnapshots(
      document({ obj: { a: 1, b: 2 }, list: [1, 2] }),
      document({ obj: { b: 2, a: 1 }, list: [2, 1] }),
      "fullState",
    ).changes.map((item) => item.key),
  ).toEqual(["list"]);
});

it("compares recorded root null as data rather than an absent snapshot", () => {
  expect(
    compareSnapshots(document(null), document(false), "fullState").changes,
  ).toEqual([{ key: "Value", before: null, after: false, kind: "changed" }]);
  expect(
    compareSnapshots(document(null), document({ a: 1 }), "variableDelta")
      .unavailable,
  ).toContain("requires object");
});

it.each<VariableDocument>([
  { status: "notRecorded" },
  { status: "contentTooLarge", sizeBytes: 6000000 },
])("refuses unavailable %j snapshots", (input) => {
  expect(
    compareSnapshots(input, document({}), "fullState").unavailable,
  ).not.toBeNull();
});

it("bounds bytes, visited values and nesting before comparison", () => {
  let nested: unknown = 1;
  for (let n = 0; n < 70; n++) nested = { value: nested };
  for (const value of [
    nested,
    Array.from({ length: 10001 }, () => 1),
    "x".repeat(262144),
  ]) {
    expect(
      compareSnapshots(document(value), document({}), "fullState").unavailable,
    ).toContain("processing limit");
  }
});

it("reports truncated results after 100 changed keys", () => {
  const result = compareSnapshots(
    document({}),
    document(
      Object.fromEntries(Array.from({ length: 120 }, (_, n) => [String(n), n])),
    ),
    "variableDelta",
  );
  expect(result.changes).toHaveLength(100);
  expect(result.truncated).toBe(true);
});

it("labels unknown semantics and concurrent full-state changes honestly", () => {
  const view = render(
    <SnapshotComparison
      input={document({ a: 1 })}
      output={document({ a: 2 })}
      interpretation="unknown"
    />,
  );
  expect(screen.getByText(/Unknown snapshot semantics/)).toBeVisible();
  view.rerender(
    <SnapshotComparison
      input={document({ a: 1 })}
      output={document({ a: 2 })}
      interpretation="fullState"
    />,
  );
  expect(screen.getByText(/concurrent branches can contribute/)).toBeVisible();
});
