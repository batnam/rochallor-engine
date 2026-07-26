import { useQuery } from "@tanstack/react-query";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";

import { listWorkflowDefinitions } from "../workflowDefinitions";
import type { Navigation } from "./Navigation";

interface Incident {
  id: string;
  processInstanceId: string;
  definitionId: string;
  definitionVersion: number;
  definitionName: string;
  stepId: string;
  stepType: string;
  attemptNumber: number;
  occurredAt: string;
  job: {
    id: string;
    type: string;
    status: string;
  } | null;
}

interface IncidentListResponse {
  items: Incident[];
  nextCursor: string | null;
}

interface IncidentDetailResponse {
  incident: Incident & {
    errorDetails: string | null;
    processInstance: {
      id: string;
      status: string;
      businessKey: string | null;
    };
  };
}

interface IncidentFilters {
  definitionId: string;
  from: string;
  jobType: string;
  to: string;
}

async function listIncidents(search: string): Promise<IncidentListResponse> {
  const response = await fetch(`/api/v1/incidents${search}`);
  if (!response.ok) {
    throw new Error("Unable to load Incidents");
  }
  return response.json() as Promise<IncidentListResponse>;
}

async function getIncidentDetail(
  incidentId: string,
): Promise<IncidentDetailResponse> {
  const response = await fetch(
    `/api/v1/incidents/${encodeURIComponent(incidentId)}`,
  );
  if (!response.ok) {
    throw new Error("Unable to load Incident");
  }
  return response.json() as Promise<IncidentDetailResponse>;
}

function filtersFromSearch(search: string): IncidentFilters {
  const parameters = new URLSearchParams(search);
  return {
    definitionId: parameters.get("definitionId") ?? "",
    from: parameters.get("from") ?? "",
    jobType: parameters.get("jobType") ?? "",
    to: parameters.get("to") ?? "",
  };
}

export function IncidentsRoute({
  incidentId = null,
  navigation,
  search,
}: {
  incidentId?: string | null;
  navigation: Navigation;
  search: string;
}): ReactNode {
  return incidentId === null ? (
    <IncidentList navigation={navigation} search={search} />
  ) : (
    <IncidentDetail incidentId={incidentId} navigation={navigation} />
  );
}

function IncidentDetail({
  incidentId,
  navigation,
}: {
  incidentId: string;
  navigation: Navigation;
}): ReactNode {
  const incidentDetail = useQuery({
    queryKey: ["incident", incidentId],
    queryFn: () => getIncidentDetail(incidentId),
    retry: false,
  });

  if (incidentDetail.isPending) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card" aria-live="polite">
          <span className="rm-skeleton rm-skeleton--heading" />
          <span className="rm-skeleton" />
          <span className="rm-visually-hidden">Loading Incident…</span>
        </section>
      </main>
    );
  }
  if (incidentDetail.isError) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card rm-state-card--error">
          <h1>Unable to load Incident</h1>
          <p>Check the Monitor API connection and try again.</p>
          <button
            className="rm-button"
            type="button"
            onClick={() => void incidentDetail.refetch()}
          >
            Retry
          </button>
        </section>
      </main>
    );
  }
  const { incident } = incidentDetail.data;
  const processInstancePath =
    `/process-instances/${encodeURIComponent(incident.processInstance.id)}` +
    `?stepId=${encodeURIComponent(incident.stepId)}`;
  return (
    <main className="rm-page">
      <header className="rm-page-header rm-page-header--detail">
        <div>
          <span className="rm-eyebrow">Execution failure</span>
          <h2>Incident {incident.id}</h2>
        </div>
        <span className="rm-status rm-status--failed">INCIDENT</span>
      </header>
      <dl className="rm-summary-grid rm-summary-grid--wide">
        <div className="rm-card rm-summary-card">
          <dt>Process Instance Status</dt>
          <dd>
            <span
              className={`rm-status rm-status--${incident.processInstance.status.toLowerCase()}`}
            >
              {incident.processInstance.status}
            </span>
          </dd>
        </div>
        <div className="rm-card rm-summary-card">
          <dt>Workflow Definition</dt>
          <dd>
            {incident.definitionName} v{incident.definitionVersion}
          </dd>
        </div>
        <div className="rm-card rm-summary-card">
          <dt>Step</dt>
          <dd className="rm-mono">{incident.stepId}</dd>
          <span>{incident.stepType}</span>
        </div>
        <div className="rm-card rm-summary-card">
          <dt>Attempt</dt>
          <dd>{incident.attemptNumber}</dd>
          <span>{incident.occurredAt}</span>
        </div>
      </dl>
      <div className="rm-detail-grid">
        <section className="rm-card rm-error-card">
          <div className="rm-card-header">
            <div>
              <span className="rm-eyebrow">Failure context</span>
              <h3>Error Details</h3>
            </div>
          </div>
          <pre>{incident.errorDetails ?? "Not recorded"}</pre>
        </section>
        <aside className="rm-card rm-context-card">
          <span className="rm-eyebrow">Related execution</span>
          <h3>Process context</h3>
          <dl className="rm-definition-list">
            <dt>Business Key</dt>
            <dd>{incident.processInstance.businessKey ?? "None"}</dd>
            <dt>Job Type</dt>
            <dd>{incident.job?.type ?? "Not applicable"}</dd>
            {incident.job ? (
              <>
                <dt>Job Status</dt>
                <dd>{incident.job.status}</dd>
              </>
            ) : null}
          </dl>
          <a
            className="rm-button rm-button--primary"
            href={processInstancePath}
            onClick={(event) => {
              event.preventDefault();
              navigation.push(processInstancePath);
            }}
          >
            Process Instance {incident.processInstance.id}
          </a>
        </aside>
      </div>
    </main>
  );
}

function IncidentList({
  navigation,
  search,
}: {
  navigation: Navigation;
  search: string;
}): ReactNode {
  const [filters, setFilters] = useState(() => filtersFromSearch(search));
  const incidents = useQuery({
    queryKey: ["incidents", search],
    queryFn: () => listIncidents(search),
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

  if (incidents.isPending) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card" aria-live="polite">
          <span className="rm-skeleton rm-skeleton--heading" />
          <span className="rm-skeleton" />
          <span className="rm-skeleton rm-skeleton--short" />
          <span className="rm-visually-hidden">Loading Incidents…</span>
        </section>
      </main>
    );
  }
  if (incidents.isError && !incidents.data) {
    return (
      <main className="rm-page">
        <section className="rm-card rm-state-card rm-state-card--error">
          <h1>Unable to load Incidents</h1>
          <p>Check the Monitor API connection and try again.</p>
          <button
            className="rm-button"
            type="button"
            onClick={() => void incidents.refetch()}
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
    if (filters.jobType) {
      parameters.set("jobType", filters.jobType);
    }
    if (filters.from) {
      parameters.set("from", filters.from);
    }
    if (filters.to) {
      parameters.set("to", filters.to);
    }
    const nextSearch = parameters.size > 0 ? `?${parameters}` : "";
    navigation.push(`/incidents${nextSearch}`);
  };
  const moveToCursor = (cursor: string | null): void => {
    const parameters = new URLSearchParams(search);
    if (cursor) {
      parameters.set("cursor", cursor);
    } else {
      parameters.delete("cursor");
    }
    const nextSearch = parameters.size > 0 ? `?${parameters}` : "";
    navigation.push(`/incidents${nextSearch}`);
  };
  const hasCursor = new URLSearchParams(search).has("cursor");

  return (
    <main className="rm-page">
      <header className="rm-page-header">
        <div>
          <span className="rm-eyebrow">Failure overview</span>
          <h2>Incidents</h2>
          <p>Investigate failed Step Executions and their context.</p>
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
              {workflowDefinitions.data?.items.map((definition) => (
                <option key={definition.id} value={definition.id}>
                  {definition.name}
                </option>
              ))}
            </select>
          </label>
          <label className="rm-field">
            <span>Job Type</span>
            <input
              value={filters.jobType}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  jobType: event.target.value,
                }))
              }
            />
          </label>
          <label className="rm-field">
            <span>Occurred From (UTC)</span>
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
            <span>Occurred To (UTC)</span>
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
            Apply Incident Filters
          </button>
        </form>

        <section className="rm-card rm-data-card">
          <div className="rm-card-header">
            <div>
              <span className="rm-eyebrow">Operational failures</span>
              <h3>Incident log</h3>
            </div>
          </div>
          {incidents.isError ? (
            <output className="rm-banner rm-banner--warning">
              Stale Incident data
            </output>
          ) : null}
          {incidents.data.items.length === 0 ? (
            <div className="rm-empty-state">
              <h4>No Incidents found</h4>
              <p>There are no failures matching the current filters.</p>
            </div>
          ) : (
            <div className="rm-table-scroll">
              <table className="rm-table">
                <thead>
                  <tr>
                    <th scope="col">Incident ID</th>
                    <th scope="col">Workflow Definition</th>
                    <th scope="col">Step</th>
                    <th scope="col">Job Type</th>
                    <th scope="col">Occurred At</th>
                  </tr>
                </thead>
                <tbody>
                  {incidents.data.items.map((incident) => (
                    <tr key={incident.id}>
                      <td>
                        <a
                          className="rm-mono-link"
                          href={`/incidents/${encodeURIComponent(incident.id)}`}
                          onClick={(event) => {
                            event.preventDefault();
                            navigation.push(
                              `/incidents/${encodeURIComponent(incident.id)}`,
                            );
                          }}
                          title={incident.id}
                        >
                          {incident.id}
                        </a>
                      </td>
                      <td>
                        {incident.definitionName} v{incident.definitionVersion}
                      </td>
                      <td className="rm-mono">{incident.stepId}</td>
                      <td>{incident.job?.type ?? "Not applicable"}</td>
                      <td className="rm-mono">{incident.occurredAt}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <footer className="rm-card-footer">
            <nav aria-label="Incident pages" className="rm-pagination">
              <button
                className="rm-button"
                type="button"
                disabled={!hasCursor}
                onClick={() => moveToCursor(null)}
              >
                Newest Incident page
              </button>
              <button
                className="rm-button"
                type="button"
                disabled={!hasCursor}
                onClick={navigation.back}
              >
                Previous Incident page
              </button>
              <button
                className="rm-button"
                type="button"
                disabled={!incidents.data.nextCursor}
                onClick={() => moveToCursor(incidents.data.nextCursor)}
              >
                Next Incident page
              </button>
            </nav>
          </footer>
        </section>
      </div>
    </main>
  );
}
