import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import {
  ExecutionContextPanel,
  type StepContext,
} from "../src/ExecutionContextPanel";

afterEach(cleanup);
const context: StepContext = {
  stepId: "service",
  executionId: "s",
  stepType: "SERVICE_TASK",
  status: "RUNNING",
  startedAt: "2026-01-01T00:00:00Z",
  reason: "jobLocked",
  unavailableReason: null,
  job: {
    id: "j",
    type: "payment",
    status: "LOCKED",
    workerId: "worker-1",
    lockedAt: "2026-01-01T00:00:01Z",
    lockExpiresAt: "2026-01-01T00:00:05Z",
    createdAt: "2026-01-01T00:00:00Z",
    retriesRemaining: 0,
  },
  task: null,
  timers: [
    {
      id: "timer",
      fireAt: "2026-01-01T00:00:08Z",
      targetStepId: "escalate",
      interrupting: true,
    },
  ],
  timersTruncated: false,
};

it("reports expired evidence at observation time without guessing worker health or retry ETA", () => {
  render(
    <ExecutionContextPanel
      contexts={[context]}
      observedAt="2026-01-01T00:00:10Z"
      selectedStepId={null}
    />,
  );
  expect(screen.getByText(/Lease expired at observation/)).toBeVisible();
  expect(screen.getByText(/Scheduled time passed/)).toBeVisible();
  expect(screen.getByText(/10 seconds elapsed/)).toBeVisible();
  expect(screen.getByText("worker-1")).toBeVisible();
  expect(screen.queryByText(/\bdead\b|next retry/i)).not.toBeInTheDocument();
});

it("does not age evidence using the browser clock and filters by selected step", () => {
  const view = render(
    <ExecutionContextPanel
      contexts={[context]}
      observedAt="2026-01-01T00:00:03Z"
      selectedStepId={null}
    />,
  );
  expect(screen.queryByText(/Lease expired/)).not.toBeInTheDocument();
  expect(screen.getByText(/3 seconds elapsed/)).toBeVisible();
  view.rerender(
    <ExecutionContextPanel
      contexts={[context]}
      observedAt="2026-01-01T00:00:03Z"
      selectedStepId="other"
    />,
  );
  expect(screen.getByText(/No current execution context/)).toBeVisible();
  expect(screen.queryByText("worker-1")).not.toBeInTheDocument();
});

it("shows unavailable evidence without inventing a wait duration for a completed join", () => {
  render(
    <ExecutionContextPanel
      contexts={[
        {
          ...context,
          stepId: "join",
          status: "COMPLETED",
          reason: "unavailable",
          unavailableReason: "No running execution evidence",
          job: null,
          timers: [],
        },
      ]}
      observedAt="2026-01-01T00:00:10Z"
      selectedStepId={null}
    />,
  );
  expect(screen.getByText(/Wait reason unavailable/)).toBeVisible();
  expect(screen.queryByText(/seconds elapsed/)).not.toBeInTheDocument();
});
