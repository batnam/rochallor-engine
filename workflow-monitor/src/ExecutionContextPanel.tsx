import type { ReactNode } from "react";

export interface StepContext {
  stepId: string;
  executionId: string | null;
  stepType: string | null;
  status: string | null;
  startedAt: string | null;
  reason: "signal" | "userTask" | "jobAvailable" | "jobLocked" | "unavailable";
  unavailableReason: string | null;
  job: {
    id: string;
    type: string;
    status: string;
    workerId: string | null;
    lockedAt: string | null;
    lockExpiresAt: string | null;
    createdAt: string;
    retriesRemaining: number;
  } | null;
  task: {
    id: string;
    assignee: string | null;
    assigneeGroup: string | null;
  } | null;
  timers: {
    id: string;
    fireAt: string;
    targetStepId: string;
    interrupting: boolean;
  }[];
  timersTruncated: boolean;
}

const reasons = {
  signal: "Waiting for a signal",
  userTask: "Waiting for task completion",
  jobAvailable: "Job available for acquisition",
  jobLocked: "Job locked by worker",
  unavailable: "Wait reason unavailable",
};

export function ExecutionContextPanel({
  contexts,
  observedAt,
  selectedStepId,
}: {
  contexts: StepContext[];
  observedAt: string;
  selectedStepId: string | null;
}): ReactNode {
  const visible = contexts.filter(
    (context) => !selectedStepId || context.stepId === selectedStepId,
  );
  const observed = Date.parse(observedAt);
  return (
    <section
      className="rm-card rm-context-card"
      aria-label="Current execution context"
    >
      <h3>Current execution context</h3>
      <p className="rm-muted">
        Observed at {observedAt}. Ages and deadlines refer to this observation.
      </p>
      {visible.length === 0 ? (
        <p>No current execution context for this selection.</p>
      ) : null}
      {visible.map((context) => (
        <article key={context.stepId} className="rm-context-step">
          <h4>
            {context.stepId} — {reasons[context.reason]}
          </h4>
          <p>Recorded status: {context.status ?? "Not recorded"}</p>
          {context.unavailableReason ? (
            <p>{context.unavailableReason}</p>
          ) : null}
          {context.startedAt &&
          context.status === "RUNNING" &&
          context.reason !== "unavailable" ? (
            <p>
              Step entered {context.startedAt} ·{" "}
              {Math.max(
                0,
                Math.floor((observed - Date.parse(context.startedAt)) / 1000),
              )}{" "}
              seconds elapsed
            </p>
          ) : null}
          {context.job ? (
            <dl className="rm-definition-list">
              <dt>Job</dt>
              <dd>
                {context.job.id} ({context.job.type})
              </dd>
              <dt>Created</dt>
              <dd>{context.job.createdAt}</dd>
              <dt>Retries remaining</dt>
              <dd>{context.job.retriesRemaining}</dd>
              <dt>Worker</dt>
              <dd>{context.job.workerId ?? "Not recorded"}</dd>
              <dt>Lock acquired</dt>
              <dd>{context.job.lockedAt ?? "Not recorded"}</dd>
              <dt>Lock expires</dt>
              <dd>
                {context.job.lockExpiresAt ?? "Not recorded"}
                {context.job.lockExpiresAt &&
                Date.parse(context.job.lockExpiresAt) <= observed
                  ? " — Lease expired at observation"
                  : ""}
              </dd>
            </dl>
          ) : null}
          {context.task ? (
            <dl className="rm-definition-list">
              <dt>Task</dt>
              <dd>{context.task.id}</dd>
              <dt>Assignee</dt>
              <dd>{context.task.assignee ?? "Unassigned"}</dd>
              <dt>Assignee group</dt>
              <dd>{context.task.assigneeGroup ?? "Unassigned"}</dd>
            </dl>
          ) : null}
          {context.timers.map((timer) => (
            <p key={timer.id}>
              Boundary timer {timer.id}: {timer.fireAt} → {timer.targetStepId}
              {timer.interrupting ? " (interrupting)" : " (non-interrupting)"}
              {Date.parse(timer.fireAt) <= observed
                ? " — Scheduled time passed"
                : ""}
            </p>
          ))}
          {context.timersTruncated ? (
            <p>Showing the first 100 pending timers.</p>
          ) : null}
        </article>
      ))}
    </section>
  );
}
