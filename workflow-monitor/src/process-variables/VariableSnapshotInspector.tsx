import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useEffect, useRef, useState } from "react";

import {
  SnapshotComparison,
  type SnapshotInterpretation,
} from "./SnapshotComparison";

export type VariableDocument =
  | { status: "present"; value: unknown; sizeBytes: number }
  | { status: "notRecorded" }
  | { status: "contentTooLarge"; sizeBytes: number };

export interface SnapshotExecution {
  id: string;
  stepId: string;
  attemptNumber: number;
  status: string;
  hasInputSnapshot: boolean;
  hasOutputSnapshot: boolean;
}

interface StepExecutionVariablesResponse {
  outputInterpretation?: SnapshotInterpretation;
  recordedInput: VariableDocument;
  recordedOutput: VariableDocument;
}

async function getStepExecutionVariables(
  instanceId: string,
  executionId: string,
): Promise<StepExecutionVariablesResponse> {
  const response = await fetch(
    `/api/v1/process-instances/${encodeURIComponent(instanceId)}/step-executions/${encodeURIComponent(executionId)}/variables`,
  );
  if (!response.ok) {
    throw new Error("Unable to load Variable Snapshots");
  }
  return response.json() as Promise<StepExecutionVariablesResponse>;
}

export function VariableSnapshotInspector({
  instanceId,
  execution,
  stepName,
  initiallyExpanded = false,
}: {
  instanceId: string;
  execution: SnapshotExecution;
  stepName?: string;
  initiallyExpanded?: boolean;
}): ReactNode {
  const stepLabel = stepName?.trim() || execution.stepId;
  const [expanded, setExpanded] = useState(initiallyExpanded);
  const client = useQueryClient();
  const signature = `${instanceId}:${execution.id}:${execution.status}:${execution.hasInputSnapshot}:${execution.hasOutputSnapshot}`;
  const previousSignature = useRef(signature);
  const snapshots = useQuery({
    queryKey: ["variable-snapshots", instanceId, execution.id],
    queryFn: () => getStepExecutionVariables(instanceId, execution.id),
    enabled:
      expanded && (execution.hasInputSnapshot || execution.hasOutputSnapshot),
    staleTime: execution.status === "COMPLETED" ? Number.POSITIVE_INFINITY : 0,
    retry: false,
  });
  const hasSnapshots =
    execution.hasInputSnapshot || execution.hasOutputSnapshot;
  useEffect(() => {
    if (previousSignature.current === signature) return;
    previousSignature.current = signature;
    // History refresh can reveal output after this inspector was opened.
    // Disabled queries remain on demand; expanded queries retain data on error.
    void client.invalidateQueries({
      queryKey: ["variable-snapshots", instanceId, execution.id],
      exact: true,
    });
  }, [client, instanceId, execution.id, signature]);

  return (
    <article className="rm-snapshot-card">
      <div className="rm-snapshot-header">
        <h4 title={stepLabel}>{stepLabel}</h4>
        <span
          className={`rm-status rm-status--${execution.status.toLowerCase()}`}
        >
          {execution.status}
        </span>
      </div>
      <p className="rm-muted">Attempt {execution.attemptNumber}</p>
      <div className="rm-snapshot-availability">
        <p>
          <span>Input Snapshot</span>
          <strong>
            {execution.hasInputSnapshot ? "Available" : "Not recorded"}
          </strong>
        </p>
        <p>
          <span>Output Snapshot</span>
          <strong>
            {execution.hasOutputSnapshot ? "Available" : "Not recorded"}
          </strong>
        </p>
      </div>
      {hasSnapshots ? (
        <button
          className="rm-button"
          type="button"
          onClick={() => setExpanded((current) => !current)}
        >
          {expanded ? "Collapse" : "Expand"} snapshots for {stepLabel} (attempt{" "}
          {execution.attemptNumber})
        </button>
      ) : null}
      {expanded && hasSnapshots && snapshots.isPending ? (
        <p>Loading Variable Snapshots…</p>
      ) : null}
      {expanded && snapshots.isError ? (
        snapshots.data ? (
          <output className="rm-banner rm-banner--warning">
            Stale Variable Snapshot data
          </output>
        ) : (
          <p>Unable to load Variable Snapshots.</p>
        )
      ) : null}
      {expanded && snapshots.isError ? (
        <button
          className="rm-button"
          type="button"
          onClick={() => void snapshots.refetch()}
        >
          Retry snapshots
        </button>
      ) : null}
      {expanded && snapshots.data ? (
        <>
          <SnapshotComparison
            input={snapshots.data.recordedInput}
            output={snapshots.data.recordedOutput}
            interpretation={snapshots.data.outputInterpretation ?? "unknown"}
          />
          <VariableDocumentView
            document={snapshots.data.recordedInput}
            label="Recorded Input"
          />
          <VariableDocumentView
            document={snapshots.data.recordedOutput}
            label="Recorded Output"
          />
        </>
      ) : null}
    </article>
  );
}

function VariableDocumentView({
  label,
  document,
}: {
  label: string;
  document: VariableDocument;
}): ReactNode {
  if (document.status === "notRecorded") {
    return <p>{label}: Not recorded</p>;
  }
  if (document.status === "contentTooLarge") {
    return (
      <p>
        {label}: Content too large ({document.sizeBytes} bytes).
      </p>
    );
  }
  return (
    <section className="rm-snapshot-document">
      <h4>{label}</h4>
      <VariableTable value={document.value} />
    </section>
  );
}

export function VariableTable({ value }: { value: unknown }): ReactNode {
  const entries: Array<[string, unknown]> =
    value !== null && typeof value === "object" && !Array.isArray(value)
      ? Object.entries(value)
      : [["Value", value]];
  return (
    <div className="rm-table-scroll">
      <table className="rm-table rm-variable-table">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Value</th>
            <th scope="col">Type</th>
          </tr>
        </thead>
        <tbody>
          {entries.map(([name, entryValue]) => (
            <tr key={name}>
              <td className="rm-mono">{name}</td>
              <td className="rm-mono">{JSON.stringify(entryValue)}</td>
              <td>{jsonType(entryValue)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function jsonType(value: unknown): string {
  if (value === null) {
    return "null";
  }
  if (Array.isArray(value)) {
    return "array";
  }
  return typeof value;
}
