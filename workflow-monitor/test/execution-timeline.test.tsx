import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ExecutionTimeline,
  type TimelineExecution,
} from "../src/ExecutionTimeline";

afterEach(cleanup);
const attempt: TimelineExecution = {
  id: "a",
  stepId: "work",
  attemptNumber: 1,
  status: "COMPLETED",
  startedAt: "2026-01-01T00:00:00Z",
  endedAt: "2026-01-01T00:00:10Z",
};

it("orders attempts chronologically with stable ID ties and preserves selection", () => {
  const inspect = vi.fn();
  render(
    <ExecutionTimeline
      executions={[{ ...attempt, id: "b", attemptNumber: 2 }, attempt]}
      observedAt={Date.parse("2026-01-01T00:00:20Z")}
      active={false}
      partial
      onInspect={inspect}
    />,
  );
  const buttons = screen.getAllByRole("button");
  expect(buttons.map((button) => button.textContent)).toEqual([
    "Inspect attempt a",
    "Inspect attempt b",
  ]);
  expect(screen.getByText(/Loaded history page only/)).toBeVisible();
  fireEvent.click(buttons[0]);
  expect(inspect).toHaveBeenCalledWith(attempt);
});

it("shows overlapping time bars and ages running attempts at the observation", () => {
  const view = render(
    <ExecutionTimeline
      executions={[
        attempt,
        {
          ...attempt,
          id: "parallel",
          status: "RUNNING",
          startedAt: "2026-01-01T00:00:05Z",
          endedAt: null,
        },
      ]}
      observedAt={Date.parse("2026-01-01T00:00:20Z")}
      active
      partial={false}
      onInspect={() => undefined}
    />,
  );
  expect(screen.getByText(/15 seconds elapsed at update/)).toBeVisible();
  const bars = view.container.querySelectorAll<HTMLElement>(".rm-timeline-bar");
  expect(bars[0].style.width).toBe("50%");
  expect(bars[1].style.marginLeft).toBe("25%");
  expect(bars[1].style.width).toBe("75%");
});

it("does not invent completion times for terminal instances with incomplete records", () => {
  render(
    <ExecutionTimeline
      executions={[{ ...attempt, endedAt: null, status: "RUNNING" }]}
      observedAt={Date.now()}
      active={false}
      partial={false}
      onInspect={() => undefined}
    />,
  );
  expect(screen.getByText(/Duration unavailable/)).toBeVisible();
  expect(
    screen.queryByText(/seconds elapsed at update/),
  ).not.toBeInTheDocument();
});
