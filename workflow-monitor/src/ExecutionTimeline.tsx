import type { ReactNode } from "react";

export interface TimelineExecution {
  id: string;
  stepId: string;
  attemptNumber: number;
  status: string;
  startedAt: string;
  endedAt: string | null;
}

export function ExecutionTimeline({
  executions,
  observedAt,
  active,
  partial,
  onInspect,
}: {
  executions: TimelineExecution[];
  observedAt: number;
  active: boolean;
  partial: boolean;
  onInspect: (execution: TimelineExecution) => void;
}): ReactNode {
  const sorted = [...executions].sort(
    (a, b) =>
      Date.parse(a.startedAt) - Date.parse(b.startedAt) ||
      (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
  const end = (execution: TimelineExecution) =>
    execution.endedAt
      ? Date.parse(execution.endedAt)
      : active && execution.status === "RUNNING"
        ? observedAt
        : null;
  const starts = sorted.map((item) => Date.parse(item.startedAt));
  const first = Math.min(...starts);
  const last = Math.max(
    ...sorted.map((item) => end(item) ?? Date.parse(item.startedAt)),
  );
  const span = Math.max(1, last - first);
  return (
    <section className="rm-timeline" aria-label="Execution timeline">
      <p>
        {partial
          ? "Loaded history page only; use the page controls for other attempts."
          : "All recorded step attempts are loaded."}
      </p>
      <p className="rm-muted">
        Step attempts, not job deliveries. Overlap can represent parallel work;
        gaps do not prove a wait reason. Running durations use the last
        successful history update.
      </p>
      {sorted.length === 0 ? (
        <p>No Step Executions recorded.</p>
      ) : (
        <ol>
          {sorted.map((execution) => {
            const finish = end(execution);
            const start = Date.parse(execution.startedAt);
            const duration =
              finish === null ? null : Math.max(0, finish - start);
            return (
              <li key={execution.id}>
                <button
                  className="rm-button"
                  type="button"
                  onClick={() => onInspect(execution)}
                >
                  Inspect attempt {execution.id}
                </button>
                <p>
                  {execution.stepId} · step attempt {execution.attemptNumber} ·{" "}
                  {execution.status}
                </p>
                <p>
                  {execution.startedAt} →{" "}
                  {execution.endedAt ??
                    (finish === null
                      ? "End not recorded"
                      : new Date(observedAt).toISOString())}
                  {duration === null
                    ? " · Duration unavailable"
                    : ` · ${duration / 1000} seconds${execution.endedAt ? "" : " elapsed at update"}`}
                </p>
                <div className="rm-timeline-track" aria-hidden="true">
                  <span
                    className={`rm-timeline-bar rm-status--${execution.status.toLowerCase()}`}
                    style={{
                      marginLeft: `${Math.max(0, ((start - first) / span) * 100)}%`,
                      width: `${Math.max(0.3, ((duration ?? 0) / span) * 100)}%`,
                    }}
                  />
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
