import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useEffect, useState } from "react";
import { DataFreshness } from "../DataFreshness";
import type { Navigation } from "./Navigation";
import { FilterError, fetchList } from "./listFilters";

interface Page<T> {
  observedAt: string;
  scope: string;
  items: T[];
  nextCursor: string | null;
}
interface DefinitionCount {
  definitionId: string;
  definitionVersion: number;
  name: string;
  active: number;
  waiting: number;
  failed: number;
}
interface StepCount {
  stepId: string;
  instances: number;
  aged: number;
  ageUnavailable: number;
}
interface StepPage extends Page<StepCount> {
  stepStartedBefore: string;
  minStepAgeSeconds: number;
}

export function OverviewRoute({
  navigation,
  search,
}: { navigation: Navigation; search: string }): ReactNode {
  const parameters = new URLSearchParams(search);
  const workflow = parameters.get("workflow") ?? "";
  const [workflowInput, setWorkflowInput] = useState(workflow);
  useEffect(() => setWorkflowInput(workflow), [workflow]);
  const definitionId = parameters.get("definitionId") ?? "";
  const definitionVersion = parameters.get("definitionVersion") ?? "";
  const age = parameters.get("minStepAgeSeconds") ?? "1800";
  const [ageInput, setAgeInput] = useState(age);
  useEffect(() => setAgeInput(age), [age]);
  const definitionParameters = new URLSearchParams();
  if (workflow) definitionParameters.set("workflow", workflow);
  const cursor = parameters.get("cursor");
  if (cursor) definitionParameters.set("cursor", cursor);
  const definitionsSearch = definitionParameters.toString();
  const stepParameters = new URLSearchParams({
    definitionId,
    definitionVersion,
    minStepAgeSeconds: age,
  });
  const stepCursor = parameters.get("stepCursor");
  if (stepCursor) stepParameters.set("cursor", stepCursor);
  const stepsSearch = stepParameters.toString();
  const definitions = useQuery({
    queryKey: ["overview", definitionsSearch],
    queryFn: () =>
      fetchList<Page<DefinitionCount>>(
        `/api/v1/overview?${definitionsSearch}`,
        "Overview",
      ),
    refetchInterval: (query) =>
      query.state.error instanceof FilterError ? false : 15_000,
    refetchIntervalInBackground: false,
    retry: false,
  });
  const steps = useQuery({
    queryKey: ["overview-steps", stepsSearch],
    queryFn: () =>
      fetchList<StepPage>(
        `/api/v1/overview/steps?${stepsSearch}`,
        "step overview",
      ),
    enabled: Boolean(definitionId && definitionVersion),
    refetchInterval: (query) =>
      query.state.error instanceof FilterError ? false : 15_000,
    refetchIntervalInBackground: false,
    retry: false,
  });
  const updateLocation = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(search);
    for (const [name, value] of Object.entries(updates)) {
      if (value === null) next.delete(name);
      else next.set(name, value);
    }
    navigation.push(`/overview?${next}`);
  };
  const searchWorkflows = (value: string) => {
    const nextWorkflow = value.trim();
    setWorkflowInput(nextWorkflow);
    updateLocation({
      workflow: nextWorkflow || null,
      cursor: null,
      definitionId: null,
      definitionVersion: null,
      stepCursor: null,
    });
  };
  const link = (href: string, text: ReactNode) => (
    <a
      href={href}
      onClick={(event) => {
        event.preventDefault();
        navigation.push(href);
      }}
    >
      {text}
    </a>
  );
  const instanceLink = (
    id: string,
    version: number | string,
    statuses: string[],
    count: number,
    stepId?: string,
    cutoff?: string,
  ) => {
    const query = new URLSearchParams({
      definitionId: id,
      definitionVersion: String(version),
    });
    for (const status of statuses) query.append("status", status);
    if (stepId) query.set("currentStepId", stepId);
    if (cutoff) query.set("stepStartedBefore", cutoff);
    return link(`/?${query}`, count);
  };
  return (
    <main className="rm-page">
      <header className="rm-page-header">
        <div>
          <h2>Overview</h2>
          <p>
            Current counts across all retained instances, including those
            started long ago.
          </p>
        </div>
        <button
          className="rm-button"
          type="button"
          onClick={() => {
            void definitions.refetch();
            if (definitionId && definitionVersion) void steps.refetch();
          }}
        >
          Refresh Overview
        </button>
      </header>
      <section className="rm-card rm-data-card" aria-label="Workflow counts">
        <div className="rm-card-header">
          <h3>Workflows by version</h3>
          <DataFreshness
            label="Workflow counts"
            updatedAt={definitions.dataUpdatedAt}
          />
        </div>
        <form
          className="rm-overview-search"
          onSubmit={(event) => {
            event.preventDefault();
            searchWorkflows(workflowInput);
          }}
        >
          <label className="rm-field">
            <span>Search workflows</span>
            <input
              type="search"
              placeholder="Workflow name or ID"
              value={workflowInput}
              onChange={(event) => setWorkflowInput(event.target.value)}
            />
          </label>
          <button className="rm-button rm-button--primary" type="submit">
            Search
          </button>
          <button
            className="rm-button"
            type="button"
            disabled={!workflow && !workflowInput}
            onClick={() => searchWorkflows("")}
          >
            Clear search
          </button>
        </form>
        {definitions.isPending ? <p>Loading workflow counts…</p> : null}
        {definitions.isError || definitions.fetchStatus === "paused" ? (
          <p role="alert">
            {definitions.data ? "Stale workflow counts. " : ""}
            {definitions.error?.message ?? "Waiting for connection."}
          </p>
        ) : null}
        {definitions.data ? (
          <>
            <p className="rm-muted">
              Observed at {definitions.data.observedAt}. Counts open a fresh
              instance search.
            </p>
            {definitions.data.items.length === 0 ? (
              <p>
                {workflow
                  ? "No workflows match your search."
                  : "No retained instances."}
              </p>
            ) : (
              <div className="rm-table-scroll">
                <table className="rm-table">
                  <thead>
                    <tr>
                      <th>Workflow</th>
                      <th>Version</th>
                      <th>ACTIVE</th>
                      <th>WAITING</th>
                      <th>FAILED</th>
                      <th>Current steps</th>
                    </tr>
                  </thead>
                  <tbody>
                    {definitions.data.items.map((row) => (
                      <tr key={`${row.definitionId}:${row.definitionVersion}`}>
                        <td>
                          {row.name}
                          <div className="rm-mono">{row.definitionId}</div>
                        </td>
                        <td>{row.definitionVersion}</td>
                        <td>
                          {instanceLink(
                            row.definitionId,
                            row.definitionVersion,
                            ["ACTIVE"],
                            row.active,
                          )}
                        </td>
                        <td>
                          {instanceLink(
                            row.definitionId,
                            row.definitionVersion,
                            ["WAITING"],
                            row.waiting,
                          )}
                        </td>
                        <td>
                          {instanceLink(
                            row.definitionId,
                            row.definitionVersion,
                            ["FAILED"],
                            row.failed,
                          )}
                        </td>
                        <td>
                          <button
                            type="button"
                            className="rm-button"
                            onClick={() =>
                              updateLocation({
                                definitionId: row.definitionId,
                                definitionVersion: String(
                                  row.definitionVersion,
                                ),
                                stepCursor: null,
                              })
                            }
                          >
                            Inspect {row.definitionId} v{row.definitionVersion}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        ) : null}
        <nav
          className="rm-card-footer rm-pagination"
          aria-label="Workflow overview pages"
        >
          <button
            className="rm-button"
            type="button"
            disabled={!cursor}
            onClick={() => updateLocation({ cursor: null })}
          >
            First workflow page
          </button>
          <button
            className="rm-button"
            type="button"
            disabled={!definitions.data?.nextCursor}
            onClick={() =>
              updateLocation({ cursor: definitions.data?.nextCursor ?? null })
            }
          >
            Next workflow page
          </button>
        </nav>
      </section>
      {definitionId && definitionVersion ? (
        <section
          className="rm-card rm-data-card"
          aria-label="Current step counts"
        >
          <div className="rm-card-header">
            <h3>
              Current steps — {definitionId} v{definitionVersion}
            </h3>
            <DataFreshness
              label="Step counts"
              updatedAt={steps.dataUpdatedAt}
            />
          </div>
          <form
            className="rm-card-header"
            onSubmit={(event) => {
              event.preventDefault();
              updateLocation({ minStepAgeSeconds: ageInput, stepCursor: null });
            }}
          >
            <label className="rm-field">
              Minimum execution age (seconds)
              <input
                type="number"
                min="1"
                max="2147483647"
                required
                value={ageInput}
                onChange={(event) => setAgeInput(event.target.value)}
              />
            </label>
            <button className="rm-button" type="submit">
              Apply age threshold
            </button>
          </form>
          <p className="rm-muted">
            An investigation threshold, not an SLA. Parallel instances appear at
            multiple steps; do not add step counts together.
          </p>
          {steps.isPending ? <p>Loading step counts…</p> : null}
          {steps.isError || steps.fetchStatus === "paused" ? (
            <p role="alert">
              {steps.data ? "Stale step counts. " : ""}
              {steps.error?.message ?? "Waiting for connection."}
            </p>
          ) : null}
          {steps.data ? (
            <>
              <p className="rm-muted">
                Observed at {steps.data.observedAt}. Aged executions started
                before {steps.data.stepStartedBefore}.
              </p>
              {steps.data.items.length === 0 ? (
                <p>No current steps.</p>
              ) : (
                <div className="rm-table-scroll">
                  <table className="rm-table">
                    <thead>
                      <tr>
                        <th>Step</th>
                        <th>Current instances</th>
                        <th>Above age threshold</th>
                        <th>Age unavailable</th>
                      </tr>
                    </thead>
                    <tbody>
                      {steps.data.items.map((row) => (
                        <tr key={row.stepId}>
                          <td>{row.stepId}</td>
                          <td>
                            {instanceLink(
                              definitionId,
                              definitionVersion,
                              ["ACTIVE", "WAITING"],
                              row.instances,
                              row.stepId,
                            )}
                          </td>
                          <td>
                            {instanceLink(
                              definitionId,
                              definitionVersion,
                              ["ACTIVE", "WAITING"],
                              row.aged,
                              row.stepId,
                              steps.data.stepStartedBefore,
                            )}
                          </td>
                          <td>{row.ageUnavailable}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          ) : null}
          <nav
            className="rm-card-footer rm-pagination"
            aria-label="Step overview pages"
          >
            <button
              className="rm-button"
              type="button"
              disabled={!stepCursor}
              onClick={() => updateLocation({ stepCursor: null })}
            >
              First step page
            </button>
            <button
              className="rm-button"
              type="button"
              disabled={!steps.data?.nextCursor}
              onClick={() =>
                updateLocation({ stepCursor: steps.data?.nextCursor ?? null })
              }
            >
              Next step page
            </button>
          </nav>
        </section>
      ) : (
        <p>Select a workflow version to inspect current steps.</p>
      )}
    </main>
  );
}
