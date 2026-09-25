import { useQuery } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";

import { DataFreshness } from "../DataFreshness";

import {
  type WorkflowDefinitionOption,
  listWorkflowDefinitions,
} from "../workflowDefinitions";
import type { Navigation } from "./Navigation";
import {
  FilterError,
  fetchList,
  timeRangeError,
  utcInputValue,
} from "./listFilters";

interface ProcessInstance {
  definitionId: string;
  id: string;
  status: string;
  businessKey: string | null;
  startedAt: string;
  completedAt: string | null;
}

interface ProcessInstanceListResponse {
  items: ProcessInstance[];
  nextCursor: string | null;
}

interface ProcessInstanceFilters {
  businessKey: string;
  definitionId: string;
  from: string;
  statuses: string[];
  to: string;
}

const PROCESS_INSTANCE_STATUSES = [
  "ACTIVE",
  "WAITING",
  "COMPLETED",
  "FAILED",
  "CANCELLED",
];

async function listProcessInstances(
  search: string,
): Promise<ProcessInstanceListResponse> {
  return fetchList<ProcessInstanceListResponse>(
    `/api/v1/process-instances${search}`,
    "Process Instances",
  );
}

function filtersFromSearch(search: string): ProcessInstanceFilters {
  const parameters = new URLSearchParams(search);
  return {
    businessKey: parameters.get("businessKey") ?? "",
    definitionId: parameters.get("definitionId") ?? "",
    from: parameters.get("from") ?? "",
    statuses: parameters.getAll("status"),
    to: parameters.get("to") ?? "",
  };
}

export function ProcessInstancesRoute({
  navigation,
  search,
}: {
  navigation: Navigation;
  search: string;
}): ReactNode {
  const [filters, setFilters] = useState(() => filtersFromSearch(search));
  const [filterError, setFilterError] = useState<string | null>(null);
  const processInstances = useQuery({
    queryKey: ["process-instances", search],
    queryFn: () => listProcessInstances(search),
    refetchInterval: (query) =>
      query.state.error instanceof FilterError ? false : 5_000,
    refetchIntervalInBackground: false,
    retry: false,
  });
  const workflowDefinitions = useQuery({
    queryKey: ["workflow-definitions"],
    queryFn: listWorkflowDefinitions,
    retry: false,
  });

  useEffect(() => setFilters(filtersFromSearch(search)), [search]);

  if (processInstances.isPending) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card" aria-live="polite">
          <span className="rm-skeleton rm-skeleton--heading" />
          <span className="rm-skeleton" />
          <span className="rm-skeleton rm-skeleton--short" />
          <span className="rm-visually-hidden">Loading Process Instances…</span>
        </section>
      </main>
    );
  }

  const applyFilters = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const error = timeRangeError(filters.from, filters.to);
    setFilterError(error);
    if (error) return;
    const parameters = new URLSearchParams();
    if (filters.definitionId) {
      parameters.set("definitionId", filters.definitionId);
    }
    for (const status of filters.statuses) {
      parameters.append("status", status);
    }
    if (filters.businessKey) {
      parameters.set("businessKey", filters.businessKey);
    }
    if (filters.from) {
      parameters.set("from", filters.from);
    }
    if (filters.to) {
      parameters.set("to", filters.to);
    }
    const nextSearch = parameters.size > 0 ? `?${parameters}` : "";
    navigation.push(`/${nextSearch}`);
  };
  const moveToCursor = (cursor: string | null): void => {
    const parameters = new URLSearchParams(search);
    if (cursor) {
      parameters.set("cursor", cursor);
    } else {
      parameters.delete("cursor");
    }
    const nextSearch = parameters.size > 0 ? `?${parameters}` : "";
    navigation.push(`/${nextSearch}`);
  };
  const hasCursor = new URLSearchParams(search).has("cursor");

  return (
    <main className="rm-page">
      <header className="rm-page-header">
        <div>
          <span className="rm-eyebrow">Execution overview</span>
          <h2>Process Instances</h2>
          <p>Inspect current and completed workflow executions.</p>
        </div>
      </header>
      <div className="rm-list-layout">
        <form className="rm-card rm-filter-card" onSubmit={applyFilters}>
          <div className="rm-card-header">
            <div>
              <span className="rm-eyebrow">Refine results</span>
              <h3>Filters</h3>
            </div>
          </div>
          <label className="rm-field">
            <span>Workflow Definition</span>
            <select
              value={filters.definitionId}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  definitionId: event.target.value,
                }))
              }
            >
              <option value="">All definitions</option>
              {workflowDefinitions.data?.items.map(
                (definition: WorkflowDefinitionOption) => (
                  <option key={definition.id} value={definition.id}>
                    {definition.name}
                  </option>
                ),
              )}
            </select>
          </label>
          <fieldset className="rm-fieldset">
            <legend>Status</legend>
            <div className="rm-status-options">
              {PROCESS_INSTANCE_STATUSES.map((status) => (
                <label key={status}>
                  <input
                    type="checkbox"
                    checked={filters.statuses.includes(status)}
                    onChange={(event) =>
                      setFilters((current) => ({
                        ...current,
                        statuses: event.target.checked
                          ? [...current.statuses, status]
                          : current.statuses.filter(
                              (value) => value !== status,
                            ),
                      }))
                    }
                  />
                  {status}
                </label>
              ))}
            </div>
          </fieldset>
          <label className="rm-field">
            <span>Business Key</span>
            <input
              value={filters.businessKey}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  businessKey: event.target.value,
                }))
              }
            />
          </label>
          <label className="rm-field">
            <span>Started From (UTC)</span>
            <input
              type="datetime-local"
              step="1"
              value={filters.from.replace(/Z$/, "")}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  from: utcInputValue(event.target.value),
                }))
              }
            />
          </label>
          <label className="rm-field">
            <span>Started To (UTC)</span>
            <input
              type="datetime-local"
              step="1"
              value={filters.to.replace(/Z$/, "")}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  to: utcInputValue(event.target.value),
                }))
              }
            />
          </label>
          {filterError || processInstances.error instanceof FilterError ? (
            <p role="alert" className="rm-banner rm-banner--warning">
              {filterError ?? processInstances.error?.message}
            </p>
          ) : null}
          <button className="rm-button rm-button--primary" type="submit">
            Apply Filters
          </button>
        </form>

        <section className="rm-card rm-data-card">
          <div className="rm-card-header">
            <div>
              <span className="rm-eyebrow">Live data</span>
              <h3>Instances</h3>
              <DataFreshness
                label="Instances"
                updatedAt={processInstances.dataUpdatedAt}
              />
            </div>
            <button
              className="rm-button"
              type="button"
              onClick={() => void processInstances.refetch()}
            >
              Refresh
            </button>
          </div>
          {processInstances.isError &&
          !processInstances.data &&
          !(processInstances.error instanceof FilterError) ? (
            <div role="alert" className="rm-banner rm-banner--warning">
              <p>{processInstances.error.message}</p>
              <button
                className="rm-button"
                type="button"
                onClick={() => void processInstances.refetch()}
              >
                Retry
              </button>
            </div>
          ) : null}
          {(processInstances.isError ||
            processInstances.fetchStatus === "paused") &&
          processInstances.data ? (
            <output className="rm-banner rm-banner--warning">Stale data</output>
          ) : null}
          {processInstances.data?.items.length === 0 ? (
            <div className="rm-empty-state">
              <h4>No Process Instances found</h4>
              <p>Adjust the filters to broaden the results.</p>
            </div>
          ) : (
            <div className="rm-table-scroll">
              <table className="rm-table">
                <thead>
                  <tr>
                    <th scope="col">Instance ID</th>
                    <th scope="col">Definition ID</th>
                    <th scope="col">Status</th>
                    <th scope="col">Business Key</th>
                    <th scope="col">Started (UTC)</th>
                    <th scope="col">Elapsed</th>
                  </tr>
                </thead>
                <tbody>
                  {processInstances.data?.items.map((processInstance) => (
                    <tr key={processInstance.id}>
                      <td>
                        <a
                          className="rm-mono-link"
                          href={`/process-instances/${encodeURIComponent(processInstance.id)}`}
                          onClick={(event) => {
                            event.preventDefault();
                            navigation.push(
                              `/process-instances/${encodeURIComponent(processInstance.id)}`,
                            );
                          }}
                          title={processInstance.id}
                        >
                          {processInstance.id}
                        </a>
                      </td>
                      <td>{processInstance.definitionId}</td>
                      <td>
                        <span
                          className={`rm-status rm-status--${processInstance.status.toLowerCase()}`}
                        >
                          {processInstance.status}
                        </span>
                      </td>
                      <td>{processInstance.businessKey ?? "None"}</td>
                      <td>{processInstance.startedAt}</td>
                      <td>
                        {elapsedTime(
                          processInstance.startedAt,
                          processInstance.completedAt,
                          processInstances.dataUpdatedAt,
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <footer className="rm-card-footer">
            <nav aria-label="Process Instance pages" className="rm-pagination">
              <button
                className="rm-button"
                type="button"
                disabled={!hasCursor}
                onClick={() => moveToCursor(null)}
              >
                Newest
              </button>
              <button
                className="rm-button"
                type="button"
                disabled={!hasCursor}
                onClick={navigation.back}
              >
                Previous
              </button>
              <button
                className="rm-button"
                type="button"
                disabled={!processInstances.data?.nextCursor}
                onClick={() =>
                  moveToCursor(processInstances.data?.nextCursor ?? null)
                }
              >
                Next
              </button>
            </nav>
          </footer>
        </section>
      </div>
    </main>
  );
}

function elapsedTime(
  start: string,
  end: string | null,
  observedAt: number,
): string {
  const milliseconds = (end ? Date.parse(end) : observedAt) - Date.parse(start);
  if (!Number.isFinite(milliseconds)) return "Not recorded";
  const seconds = Math.max(0, Math.floor(milliseconds / 1_000));
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  return `${days ? `${days}d ` : ""}${hours}h ${minutes}m ${seconds % 60}s`;
}
