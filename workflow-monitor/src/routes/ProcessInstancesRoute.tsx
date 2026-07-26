import { useQuery } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";

import {
  type WorkflowDefinitionOption,
  listWorkflowDefinitions,
} from "../workflowDefinitions";
import type { Navigation } from "./Navigation";

interface ProcessInstance {
  definitionId: string;
  id: string;
  status: string;
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
  const response = await fetch(`/api/v1/process-instances${search}`);
  if (!response.ok) {
    throw new Error("Unable to load Process Instances");
  }
  return response.json() as Promise<ProcessInstanceListResponse>;
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
  const processInstances = useQuery({
    queryKey: ["process-instances", search],
    queryFn: () => listProcessInstances(search),
    refetchInterval: 5_000,
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

  if (processInstances.isError && !processInstances.data) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card rm-state-card--error">
          <h1>Unable to load Process Instances</h1>
          <p>Check the Monitor API connection and try again.</p>
          <button
            className="rm-button"
            type="button"
            onClick={() => void processInstances.refetch()}
          >
            Retry
          </button>
        </section>
      </main>
    );
  }

  const applyFilters = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
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
              value={filters.from}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  from: event.target.value,
                }))
              }
            />
          </label>
          <label className="rm-field">
            <span>Started To (UTC)</span>
            <input
              value={filters.to}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  to: event.target.value,
                }))
              }
            />
          </label>
          <button className="rm-button rm-button--primary" type="submit">
            Apply Filters
          </button>
        </form>

        <section className="rm-card rm-data-card">
          <div className="rm-card-header">
            <div>
              <span className="rm-eyebrow">Live data</span>
              <h3>Instances</h3>
            </div>
            <button
              className="rm-button"
              type="button"
              onClick={() => void processInstances.refetch()}
            >
              Refresh
            </button>
          </div>
          {processInstances.isError ? (
            <output className="rm-banner rm-banner--warning">Stale data</output>
          ) : null}
          {processInstances.data.items.length === 0 ? (
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
                  </tr>
                </thead>
                <tbody>
                  {processInstances.data.items.map((processInstance) => (
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
                disabled={!processInstances.data.nextCursor}
                onClick={() => moveToCursor(processInstances.data.nextCursor)}
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
