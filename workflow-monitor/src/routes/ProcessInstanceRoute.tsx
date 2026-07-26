import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useEffect, useState } from "react";

import {
  type DiagramStep,
  ExecutionDiagram,
  type ExecutionOverlay,
} from "../ExecutionDiagram";
import {
  type SnapshotExecution,
  type VariableDocument,
  VariableSnapshotInspector,
  VariableTable,
} from "../process-variables/VariableSnapshotInspector";

interface ProcessInstanceDetailResponse {
  instance: {
    id: string;
    status: string;
    definitionId: string;
    definitionVersion: number;
    businessKey: string | null;
  };
  definition: {
    id: string;
    version: number;
    name: string;
    steps: DiagramStep[];
  };
  executionOverlay: ExecutionOverlay;
}

interface StepExecution extends SnapshotExecution {
  stepId: string;
  stepType: string;
  attemptNumber: number;
  startedAt: string;
  endedAt: string | null;
  hasFailure: boolean;
}

interface StepExecutionListResponse {
  items: StepExecution[];
  nextCursor: string | null;
}

interface CurrentVariablesResponse {
  current: VariableDocument;
}

async function getProcessInstanceDetail(
  instanceId: string,
): Promise<ProcessInstanceDetailResponse> {
  const response = await fetch(
    `/api/v1/process-instances/${encodeURIComponent(instanceId)}`,
  );
  if (!response.ok) {
    throw new Error("Unable to load Process Instance");
  }
  return response.json() as Promise<ProcessInstanceDetailResponse>;
}

async function listStepExecutions(
  instanceId: string,
  cursor: string | null,
): Promise<StepExecutionListResponse> {
  const parameters = new URLSearchParams();
  if (cursor) {
    parameters.set("cursor", cursor);
  }
  const search = parameters.size > 0 ? `?${parameters}` : "";
  const response = await fetch(
    `/api/v1/process-instances/${encodeURIComponent(instanceId)}/step-executions${search}`,
  );
  if (!response.ok) {
    throw new Error("Unable to load Step Executions");
  }
  return response.json() as Promise<StepExecutionListResponse>;
}

async function getCurrentVariables(
  instanceId: string,
): Promise<CurrentVariablesResponse> {
  const response = await fetch(
    `/api/v1/process-instances/${encodeURIComponent(instanceId)}/variables`,
  );
  if (!response.ok) {
    throw new Error("Unable to load Current Variables");
  }
  return response.json() as Promise<CurrentVariablesResponse>;
}

export function ProcessInstanceRoute({
  instanceId,
  search,
}: {
  instanceId: string;
  search: string;
}): ReactNode {
  const [selectedStepId, setSelectedStepId] = useState<string | null>(() =>
    new URLSearchParams(search).get("stepId"),
  );
  const [stepExecutionCursor, setStepExecutionCursor] = useState<string | null>(
    null,
  );
  const [stepExecutionCursorHistory, setStepExecutionCursorHistory] = useState<
    Array<string | null>
  >([]);
  const [detailView, setDetailView] = useState<"overview" | "variables">(
    "overview",
  );
  const processInstanceDetail = useQuery({
    queryKey: ["process-instance", instanceId],
    queryFn: () => getProcessInstanceDetail(instanceId),
    retry: false,
  });
  const stepExecutions = useQuery({
    queryKey: ["step-executions", instanceId, stepExecutionCursor],
    queryFn: () => listStepExecutions(instanceId, stepExecutionCursor),
    retry: false,
  });
  const currentVariables = useQuery({
    queryKey: ["current-variables", instanceId],
    queryFn: () => getCurrentVariables(instanceId),
    enabled: detailView === "variables",
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
    retry: false,
  });

  useEffect(() => {
    setSelectedStepId(new URLSearchParams(search).get("stepId"));
  }, [search]);

  if (processInstanceDetail.isPending) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card" aria-live="polite">
          <span className="rm-skeleton rm-skeleton--heading" />
          <span className="rm-skeleton" />
          <span className="rm-skeleton rm-skeleton--short" />
          <span className="rm-visually-hidden">Loading Process Instance…</span>
        </section>
      </main>
    );
  }
  if (processInstanceDetail.isError) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card rm-state-card--error">
          <h1>Unable to load Process Instance</h1>
          <p>Check the Monitor API connection and try again.</p>
          <button
            className="rm-button"
            type="button"
            onClick={() => void processInstanceDetail.refetch()}
          >
            Retry
          </button>
        </section>
      </main>
    );
  }

  const detail = processInstanceDetail.data;
  const selectedStepName = detail.definition.steps.find(
    (step) => step.id === selectedStepId,
  )?.name;

  return (
    <main className="rm-page">
      <header className="rm-page-header rm-page-header--detail">
        <div>
          <span className="rm-eyebrow">Execution details</span>
          <h2>Process Instance {detail.instance.id}</h2>
        </div>
      </header>
      <dl className="rm-summary-grid">
        <div className="rm-card rm-summary-card">
          <dt>Status</dt>
          <dd>
            <span
              className={`rm-status rm-status--${detail.instance.status.toLowerCase()}`}
            >
              {detail.instance.status}
            </span>
          </dd>
        </div>
        <div className="rm-card rm-summary-card">
          <dt>Workflow Definition</dt>
          <dd>
            {detail.definition.name} v{detail.definition.version}
          </dd>
          <span className="rm-mono">{detail.definition.id}</span>
        </div>
        <div className="rm-card rm-summary-card">
          <dt>Business Key</dt>
          <dd>{detail.instance.businessKey ?? "None"}</dd>
        </div>
      </dl>
      <div
        role="tablist"
        aria-label="Process Instance detail views"
        className="rm-tabs"
      >
        <button
          aria-selected={detailView === "overview"}
          className={
            detailView === "overview" ? "rm-tab rm-tab--active" : "rm-tab"
          }
          onClick={() => setDetailView("overview")}
          role="tab"
          type="button"
        >
          Overview
        </button>
        <button
          aria-selected={detailView === "variables"}
          className={
            detailView === "variables" ? "rm-tab rm-tab--active" : "rm-tab"
          }
          onClick={() => setDetailView("variables")}
          role="tab"
          type="button"
        >
          Variables
        </button>
      </div>
      {detailView === "overview" ? (
        <div className="rm-tab-panel rm-overview-grid" role="tabpanel">
          <ExecutionDiagram
            onStepSelect={setSelectedStepId}
            overlay={detail.executionOverlay}
            selectedStepId={selectedStepId}
            steps={detail.definition.steps}
          />
          <section className="rm-card rm-executions-card">
            <div className="rm-card-header">
              <div>
                <span className="rm-eyebrow">Execution history</span>
                <h3>Step Executions</h3>
              </div>
              <button
                className="rm-button"
                type="button"
                onClick={() => void stepExecutions.refetch()}
              >
                Refresh Step Executions
              </button>
            </div>
            {selectedStepName ? (
              <div className="rm-banner rm-banner--info">
                <span>Highlighting Step Executions for {selectedStepName}</span>
                <button
                  className="rm-button"
                  type="button"
                  onClick={() => setSelectedStepId(null)}
                >
                  Clear Step Execution highlight
                </button>
              </div>
            ) : null}
            {stepExecutions.isPending ? <p>Loading Step Executions…</p> : null}
            {stepExecutions.isError ? (
              stepExecutions.data ? (
                <output>Stale Step Execution data</output>
              ) : (
                <p>Unable to load Step Executions.</p>
              )
            ) : null}
            {stepExecutions.data ? (
              <>
                {stepExecutions.data.items.length === 0 ? (
                  <p>No Step Executions recorded.</p>
                ) : (
                  <div className="rm-table-scroll">
                    <table className="rm-table">
                      <thead>
                        <tr>
                          <th scope="col">Execution ID</th>
                          <th scope="col">Step</th>
                          <th scope="col">Type</th>
                          <th scope="col">Attempt</th>
                          <th scope="col">Status</th>
                          <th scope="col">Started</th>
                          <th scope="col">Ended</th>
                          <th scope="col">Failure</th>
                          <th scope="col">Input Snapshot</th>
                          <th scope="col">Output Snapshot</th>
                        </tr>
                      </thead>
                      <tbody>
                        {stepExecutions.data.items.map((execution) => (
                          <tr
                            aria-current={
                              execution.stepId === selectedStepId
                                ? "true"
                                : undefined
                            }
                            key={execution.id}
                          >
                            <td>{execution.id}</td>
                            <td>{execution.stepId}</td>
                            <td>{execution.stepType}</td>
                            <td>{execution.attemptNumber}</td>
                            <td>{execution.status}</td>
                            <td>{execution.startedAt}</td>
                            <td>{execution.endedAt ?? "In progress"}</td>
                            <td>{execution.hasFailure ? "Present" : "None"}</td>
                            <td>
                              {execution.hasInputSnapshot
                                ? "Recorded"
                                : "Not recorded"}
                            </td>
                            <td>
                              {execution.hasOutputSnapshot
                                ? "Recorded"
                                : "Not recorded"}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                <nav
                  aria-label="Step Execution pages"
                  className="rm-pagination rm-card-footer"
                >
                  <button
                    className="rm-button"
                    type="button"
                    disabled={stepExecutionCursor === null}
                    onClick={() => {
                      setStepExecutionCursor(null);
                      setStepExecutionCursorHistory([]);
                    }}
                  >
                    Newest Step Execution page
                  </button>
                  <button
                    className="rm-button"
                    type="button"
                    disabled={stepExecutionCursorHistory.length === 0}
                    onClick={() => {
                      setStepExecutionCursor(
                        stepExecutionCursorHistory[
                          stepExecutionCursorHistory.length - 1
                        ] ?? null,
                      );
                      setStepExecutionCursorHistory((current) =>
                        current.slice(0, -1),
                      );
                    }}
                  >
                    Previous Step Execution page
                  </button>
                  <button
                    className="rm-button"
                    type="button"
                    disabled={!stepExecutions.data.nextCursor}
                    onClick={() => {
                      setStepExecutionCursorHistory((current) => [
                        ...current,
                        stepExecutionCursor,
                      ]);
                      setStepExecutionCursor(stepExecutions.data.nextCursor);
                    }}
                  >
                    Next Step Execution page
                  </button>
                </nav>
              </>
            ) : null}
          </section>
        </div>
      ) : (
        <section className="rm-tab-panel rm-variables-grid" role="tabpanel">
          <div className="rm-card rm-variable-card">
            <div className="rm-card-header">
              <div>
                <span className="rm-eyebrow">Latest state</span>
                <h3>Current Variables</h3>
              </div>
            </div>
            {currentVariables.isPending ? (
              <p>Loading Current Variables…</p>
            ) : null}
            {currentVariables.isError ? (
              <p>Unable to load Current Variables.</p>
            ) : null}
            {currentVariables.data?.current.status === "present" ? (
              <VariableTable value={currentVariables.data.current.value} />
            ) : null}
            {currentVariables.data?.current.status === "contentTooLarge" ? (
              <p>
                Current Variables content is too large (
                {currentVariables.data.current.sizeBytes} bytes).
              </p>
            ) : null}
          </div>
          <div className="rm-card rm-variable-card">
            <div className="rm-card-header">
              <div>
                <span className="rm-eyebrow">Recorded boundaries</span>
                <h3>Variable Snapshots</h3>
              </div>
            </div>
            <p className="rm-muted">
              Snapshots are recorded execution boundaries, not a complete
              variable-change history.
            </p>
            {stepExecutions.isPending ? <p>Loading Step Executions…</p> : null}
            {stepExecutions.isError ? (
              <p>Unable to load Step Executions.</p>
            ) : null}
            <div className="rm-snapshot-grid">
              {stepExecutions.data?.items.map((execution) => (
                <VariableSnapshotInspector
                  execution={execution}
                  instanceId={detail.instance.id}
                  key={execution.id}
                />
              ))}
            </div>
          </div>
        </section>
      )}
    </main>
  );
}
