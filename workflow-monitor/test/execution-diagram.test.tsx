import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";

import { type DiagramStep, ExecutionDiagram } from "../src/ExecutionDiagram";

afterEach(cleanup);

it("renders every supported step and edge type through the read-only diagram", async () => {
  const steps: DiagramStep[] = [
    {
      id: "service-entry",
      name: "Service Entry",
      type: "SERVICE_TASK",
      nextStep: "user-review",
      boundaryEvents: [
        {
          type: "TIMER",
          duration: "PT5M",
          interrupting: true,
          targetStepId: "timeout-transform",
        },
      ],
    },
    {
      id: "user-review",
      name: "User Review",
      type: "USER_TASK",
      nextStep: "route-decision",
    },
    {
      id: "route-decision",
      name: "Route Decision",
      type: "DECISION",
      conditionalNextSteps: {
        approved: "decision-table",
        rejected: "transform-data",
      },
    },
    {
      id: "decision-table",
      name: "Decision Table",
      type: "DECISION_TABLE",
      nextStep: "parallel-split",
    },
    {
      id: "transform-data",
      name: "Transform Data",
      type: "TRANSFORMATION",
      nextStep: "parallel-split",
    },
    {
      id: "parallel-split",
      name: "Parallel Split",
      type: "PARALLEL_GATEWAY",
      parallelNextSteps: ["branch-service", "branch-wait"],
      joinStep: "parallel-join",
    },
    {
      id: "branch-service",
      name: "Branch Service",
      type: "SERVICE_TASK",
      nextStep: "parallel-join",
    },
    {
      id: "branch-wait",
      name: "Branch Wait",
      type: "WAIT",
      nextStep: "parallel-join",
      boundaryEvents: [
        {
          type: "TIMER",
          duration: "PT1H",
          interrupting: false,
          targetStepId: "timeout-transform",
        },
      ],
    },
    {
      id: "parallel-join",
      name: "Parallel Join",
      type: "JOIN_GATEWAY",
      nextStep: "end",
    },
    {
      id: "timeout-transform",
      name: "Timeout Transform",
      type: "TRANSFORMATION",
      nextStep: "end",
    },
    { id: "end", name: "End", type: "END" },
  ];

  render(
    <ExecutionDiagram
      overlay={{
        currentTokenStepIds: [],
        failedStepId: null,
        latestByStep: [],
      }}
      steps={steps}
    />,
  );

  const diagram = await screen.findByRole("img", {
    name: "Execution Diagram",
  });
  for (const name of [
    "Service Entry",
    "User Review",
    "Route Decision",
    "Decision Table",
    "Transform Data",
    "Parallel Split",
    "Branch Service",
    "Branch Wait",
    "Parallel Join",
    "Timeout Transform",
    "End",
  ]) {
    expect(screen.getByLabelText(name)).toBeVisible();
  }

  for (const name of [
    "Service Entry to User Review, sequential edge",
    "Service Entry to Timeout Transform, boundary edge",
    "Route Decision to Decision Table, conditional edge",
    "Parallel Split to Branch Service, parallel edge",
    "Branch Service to Parallel Join, join-target edge",
    "Parallel Join to End, join-out edge",
  ]) {
    expect(screen.getByLabelText(name)).toBeVisible();
  }

  expect(
    screen.getByLabelText("Branch Service").getAttribute("transform"),
  ).not.toBe(screen.getByLabelText("Branch Wait").getAttribute("transform"));
  expect(
    screen.queryByLabelText(/Current Token Position/),
  ).not.toBeInTheDocument();
  expect(within(diagram).queryByRole("button")).not.toBeInTheDocument();
  expect(within(diagram).queryByRole("textbox")).not.toBeInTheDocument();
  expect(diagram.querySelector('[draggable="true"]')).toBeNull();
});

it("renders parallel tokens and the latest execution-state overlays", async () => {
  render(
    <ExecutionDiagram
      overlay={{
        currentTokenStepIds: ["branch-a", "branch-b"],
        failedStepId: "failed-step",
        latestByStep: [
          {
            executionId: "branch-a-attempt-2",
            stepId: "branch-a",
            status: "RUNNING",
            attemptNumber: 2,
          },
          {
            executionId: "branch-b-attempt-1",
            stepId: "branch-b",
            status: "RUNNING",
            attemptNumber: 1,
          },
          {
            executionId: "completed-attempt-1",
            stepId: "completed-step",
            status: "COMPLETED",
            attemptNumber: 1,
          },
          {
            executionId: "skipped-attempt-1",
            stepId: "skipped-step",
            status: "SKIPPED",
            attemptNumber: 1,
          },
          {
            executionId: "failed-attempt-3",
            stepId: "failed-step",
            status: "FAILED",
            attemptNumber: 3,
          },
        ],
      }}
      steps={[
        { id: "branch-a", name: "Branch A", type: "SERVICE_TASK" },
        { id: "branch-b", name: "Branch B", type: "USER_TASK" },
        {
          id: "completed-step",
          name: "Completed Step",
          type: "TRANSFORMATION",
        },
        { id: "skipped-step", name: "Skipped Step", type: "WAIT" },
        { id: "failed-step", name: "Failed Step", type: "SERVICE_TASK" },
      ]}
    />,
  );

  await screen.findByRole("img", { name: "Execution Diagram" });
  const branchA = screen.getByLabelText(
    "Branch A, Current Token Position, RUNNING, attempt 2",
  );
  const branchB = screen.getByLabelText(
    "Branch B, Current Token Position, RUNNING, attempt 1",
  );
  expect(branchA.getAttribute("transform")).not.toBe(
    branchB.getAttribute("transform"),
  );
  expect(
    screen.getByLabelText("Completed Step, COMPLETED, attempt 1"),
  ).toBeVisible();
  expect(
    screen.getByLabelText("Skipped Step, SKIPPED, attempt 1"),
  ).toBeVisible();
  expect(
    screen.getByLabelText("Failed Step, Failed marker, attempt 3"),
  ).toBeVisible();
});
