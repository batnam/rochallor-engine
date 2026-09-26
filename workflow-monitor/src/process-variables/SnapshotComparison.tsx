import { type ReactNode, useMemo } from "react";
import type { VariableDocument } from "./VariableSnapshotInspector";

export type SnapshotInterpretation = "variableDelta" | "fullState" | "unknown";
interface Change {
  key: string;
  before: unknown;
  after: unknown;
  kind: "added" | "removed" | "changed";
}
interface Comparison {
  changes: Change[];
  truncated: boolean;
  unavailable: string | null;
}
const MAX_BYTES = 256 * 1024;
const MAX_NODES = 10_000;
const MAX_CHANGES = 100;

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// Inspect the whole documents first, iteratively. Equality and value rendering
// only run on data within these limits; truncating rows alone is not a CPU bound.
function withinLimits(values: unknown[]): boolean {
  const pending = values.map((value) => ({ value, depth: 0 }));
  let visited = 0;
  while (pending.length) {
    const item = pending.pop();
    if (!item) break;
    if (++visited > MAX_NODES || item.depth > 64) return false;
    if (item.value !== null && typeof item.value === "object") {
      const children = Object.values(item.value);
      if (children.length + pending.length + visited > MAX_NODES) return false;
      for (const value of children)
        pending.push({ value, depth: item.depth + 1 });
    }
  }
  return true;
}

function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (
    a === null ||
    b === null ||
    typeof a !== "object" ||
    typeof b !== "object"
  )
    return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((value, i) => equal(value, b[i]))
    );
  }
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  return (
    Object.keys(left).length === Object.keys(right).length &&
    Object.keys(left).every(
      (key) => Object.hasOwn(right, key) && equal(left[key], right[key]),
    )
  );
}

export function compareSnapshots(
  input: VariableDocument,
  output: VariableDocument,
  interpretation: SnapshotInterpretation,
): Comparison {
  const unavailable = (message: string): Comparison => ({
    changes: [],
    truncated: false,
    unavailable: message,
  });
  if (input.status !== "present" || output.status !== "present")
    return unavailable(
      "Both snapshots must be recorded and within the document size limit.",
    );
  if (
    input.sizeBytes + output.sizeBytes > MAX_BYTES ||
    !withinLimits([input.value, output.value])
  ) {
    return unavailable(
      "Comparison exceeds the 256 KiB, 10,000-value or 64-level processing limit. Raw snapshots remain available.",
    );
  }
  if (
    interpretation === "variableDelta" &&
    (!record(input.value) || !record(output.value))
  ) {
    return unavailable(
      "Returned-variable comparison requires object snapshots. Inspect the raw values.",
    );
  }
  let changes: Change[];
  if (record(input.value) && record(output.value)) {
    const before = input.value;
    const after = output.value;
    const keys =
      interpretation === "variableDelta"
        ? Object.keys(after)
        : [...new Set([...Object.keys(before), ...Object.keys(after)])];
    changes = keys.sort().flatMap((key): Change[] => {
      const had = Object.hasOwn(before, key);
      const has = Object.hasOwn(after, key);
      if (had && has && equal(before[key], after[key])) return [];
      return [
        {
          key,
          before: before[key],
          after: after[key],
          kind: !had ? "added" : !has ? "removed" : "changed",
        },
      ];
    });
  } else {
    changes = equal(input.value, output.value)
      ? []
      : [
          {
            key: "Value",
            before: input.value,
            after: output.value,
            kind: "changed",
          },
        ];
  }
  return {
    changes: changes.slice(0, MAX_CHANGES),
    truncated: changes.length > MAX_CHANGES,
    unavailable: null,
  };
}

function Value({ value }: { value: unknown }): ReactNode {
  if (value === undefined) return <>Missing</>;
  const json = JSON.stringify(value);
  if (json.length <= 160) return <code>{json}</code>;
  return (
    <details>
      <summary>Show JSON value ({json.length} characters)</summary>
      <pre>{json}</pre>
    </details>
  );
}

export function SnapshotComparison({
  input,
  output,
  interpretation,
}: {
  input: VariableDocument;
  output: VariableDocument;
  interpretation: SnapshotInterpretation;
}): ReactNode {
  const result = useMemo(
    () => compareSnapshots(input, output, interpretation),
    [input, output, interpretation],
  );
  return (
    <section className="rm-snapshot-document" aria-label="Snapshot comparison">
      <h4>
        {interpretation === "variableDelta"
          ? "Returned-variable changes"
          : "Recorded document differences"}
      </h4>
      <p className="rm-muted">
        {interpretation === "variableDelta"
          ? "Only returned keys are compared. Omitted keys are not deletions. This does not reconstruct the global post-step state."
          : interpretation === "fullState"
            ? "Observed differences between recorded states; concurrent branches can contribute changes."
            : "Unknown snapshot semantics: raw document differences only, not proof of variable changes."}
      </p>
      {result.unavailable ? (
        <p>Comparison unavailable: {result.unavailable}</p>
      ) : (
        <>
          {result.changes.length === 0 ? (
            <p>No differences in the compared values.</p>
          ) : (
            <div className="rm-table-scroll">
              <table className="rm-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Difference</th>
                    <th>Recorded input</th>
                    <th>Recorded output</th>
                  </tr>
                </thead>
                <tbody>
                  {result.changes.map((change) => (
                    <tr key={change.key}>
                      <td>{change.key}</td>
                      <td>{change.kind}</td>
                      <td>
                        <Value value={change.before} />
                      </td>
                      <td>
                        <Value value={change.after} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {result.truncated ? <p>Showing the first 100 differences.</p> : null}
        </>
      )}
    </section>
  );
}
