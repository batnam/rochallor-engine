import ELK, {
  type ElkExtendedEdge,
  type ElkNode,
} from "elkjs/lib/elk.bundled.js";
import { type ReactNode, useEffect, useState } from "react";

export interface DiagramStep {
  id: string;
  name: string;
  type: string;
  nextStep?: string;
  [key: string]: unknown;
}

export interface ExecutionOverlay {
  currentTokenStepIds: string[];
  failedStepId: string | null;
  latestByStep: Array<{
    executionId: string;
    stepId: string;
    status: string;
    attemptNumber: number;
  }>;
}

interface DiagramEdge extends ElkExtendedEdge {
  sourceName: string;
  targetName: string;
  variant:
    | "sequential"
    | "conditional"
    | "parallel"
    | "join-target"
    | "join-out"
    | "boundary";
}

interface DiagramLayout extends ElkNode {
  children: ElkNode[];
  edges: DiagramEdge[];
}

const elk = new ELK();

export function ExecutionDiagram({
  steps,
  overlay,
  selectedStepId,
  onStepSelect,
}: {
  steps: DiagramStep[];
  overlay: ExecutionOverlay;
  selectedStepId?: string | null;
  onStepSelect?(stepId: string): void;
}): ReactNode {
  const [layout, setLayout] = useState<DiagramLayout | null>(null);

  useEffect(() => {
    let cancelled = false;
    const stepById = new Map(steps.map((step) => [step.id, step]));
    const edges = buildEdges(steps, stepById);
    const graph: DiagramLayout = {
      id: "execution-diagram",
      layoutOptions: {
        "elk.algorithm": "layered",
        "elk.direction": "RIGHT",
        "elk.spacing.nodeNode": "40",
      },
      children: steps.map((step) => ({
        id: step.id,
        width: 180,
        height: 70,
      })),
      edges,
    };

    void elk.layout(graph).then((result) => {
      if (!cancelled) {
        setLayout(result as DiagramLayout);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [steps]);

  if (!layout) {
    return (
      <section className="rm-card rm-diagram-card" aria-live="polite">
        <div className="rm-card-header">
          <div>
            <span className="rm-eyebrow">Current flow</span>
            <h3>Execution Diagram</h3>
          </div>
        </div>
        <p className="rm-muted">Layouting execution diagram…</p>
      </section>
    );
  }

  const stepById = new Map(steps.map((step) => [step.id, step]));
  const diagramWidth = Math.max(layout.width ?? 1, 1200);
  const diagramHeight = Math.max(layout.height ?? 1, 280);
  return (
    <section className="rm-card rm-diagram-card">
      <div className="rm-card-header">
        <div>
          <span className="rm-eyebrow">Current flow</span>
          <h3>Execution Diagram</h3>
        </div>
        <span className="rm-muted">{steps.length} steps</span>
      </div>
      <div className="rm-diagram-canvas">
        <svg
          aria-label="Execution Diagram"
          className="rm-diagram"
          height={diagramHeight}
          role={onStepSelect ? "group" : "img"}
          viewBox={`0 0 ${diagramWidth} ${diagramHeight}`}
          width={diagramWidth}
        >
          {layout.edges.map((edge) => (
            <path
              aria-label={`${edge.sourceName} to ${edge.targetName}, ${edge.variant} edge`}
              className={`rm-diagram-edge rm-diagram-edge--${edge.variant}`}
              d={edgePath(edge)}
              fill="none"
              key={edge.id}
            />
          ))}
          {layout.children.map((node) => {
            const step = stepById.get(node.id);
            if (!step) {
              return null;
            }
            const isCurrent = overlay.currentTokenStepIds.includes(step.id);
            const isFailed = overlay.failedStepId === step.id;
            const isSelected = selectedStepId === step.id;
            const execution = overlay.latestByStep.find(
              (candidate) => candidate.stepId === step.id,
            );
            const stateLabel = isFailed
              ? `Failed marker${execution ? `, attempt ${execution.attemptNumber}` : ""}`
              : isCurrent
                ? `Current Token Position${execution ? `, ${execution.status}, attempt ${execution.attemptNumber}` : ""}`
                : execution
                  ? `${execution.status}, attempt ${execution.attemptNumber}`
                  : null;
            const label = stateLabel
              ? `${step.name}, ${stateLabel}`
              : step.name;
            const visualStateLabel = execution
              ? `${execution.status} · attempt ${execution.attemptNumber}`
              : isFailed
                ? "Failed"
                : isCurrent
                  ? "Current Token Position"
                  : step.type;
            const stateClass = isFailed
              ? "failed"
              : isCurrent
                ? "current"
                : (execution?.status.toLowerCase() ?? "idle");
            return (
              <g
                aria-label={label}
                aria-pressed={onStepSelect ? isSelected : undefined}
                className={`rm-diagram-node rm-diagram-node--${stateClass}${isSelected ? " rm-diagram-node--selected" : ""}`}
                key={step.id}
                onClick={onStepSelect ? () => onStepSelect(step.id) : undefined}
                onKeyDown={
                  onStepSelect
                    ? (event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          onStepSelect(step.id);
                        }
                      }
                    : undefined
                }
                role={onStepSelect ? "button" : undefined}
                tabIndex={onStepSelect ? 0 : undefined}
                transform={`translate(${node.x ?? 0} ${node.y ?? 0})`}
              >
                <rect
                  className="rm-diagram-node-shape"
                  height={node.height}
                  rx="8"
                  width={node.width}
                />
                <text className="rm-diagram-node-title" x={12} y={28}>
                  {step.name}
                </text>
                <text className="rm-diagram-node-meta" x={12} y={50}>
                  {visualStateLabel}
                </text>
              </g>
            );
          })}
        </svg>
      </div>
    </section>
  );
}

function buildEdges(
  steps: DiagramStep[],
  stepById: Map<string, DiagramStep>,
): DiagramEdge[] {
  const edges: DiagramEdge[] = [];
  const addEdge = (
    source: DiagramStep,
    targetId: string,
    variant: DiagramEdge["variant"],
    discriminator: string = variant,
  ): void => {
    const target = stepById.get(targetId);
    if (!target) {
      return;
    }
    edges.push({
      id: `${source.id}--${discriminator}-->${target.id}`,
      sources: [source.id],
      targets: [target.id],
      sourceName: source.name,
      targetName: target.name,
      variant,
    });
  };

  for (const step of steps) {
    if (step.nextStep) {
      const target = stepById.get(step.nextStep);
      const variant =
        step.type === "JOIN_GATEWAY"
          ? "join-out"
          : target?.type === "JOIN_GATEWAY"
            ? "join-target"
            : "sequential";
      addEdge(step, step.nextStep, variant);
    }
    if (
      step.type === "DECISION" &&
      step.conditionalNextSteps &&
      typeof step.conditionalNextSteps === "object"
    ) {
      for (const [expression, targetId] of Object.entries(
        step.conditionalNextSteps,
      )) {
        if (typeof targetId === "string") {
          addEdge(step, targetId, "conditional", `conditional:${expression}`);
        }
      }
    }
    if (
      step.type === "PARALLEL_GATEWAY" &&
      Array.isArray(step.parallelNextSteps)
    ) {
      for (const [index, targetId] of step.parallelNextSteps.entries()) {
        if (typeof targetId === "string") {
          addEdge(step, targetId, "parallel", `parallel:${index}`);
        }
      }
    }
    if (Array.isArray(step.boundaryEvents)) {
      for (const [index, boundaryEvent] of step.boundaryEvents.entries()) {
        if (
          boundaryEvent &&
          typeof boundaryEvent === "object" &&
          "targetStepId" in boundaryEvent &&
          typeof boundaryEvent.targetStepId === "string"
        ) {
          addEdge(
            step,
            boundaryEvent.targetStepId,
            "boundary",
            `boundary:${index}`,
          );
        }
      }
    }
  }
  return edges;
}

function edgePath(edge: ElkExtendedEdge): string {
  const section = edge.sections?.[0];
  if (!section) {
    return "";
  }
  const points = [
    section.startPoint,
    ...(section.bendPoints ?? []),
    section.endPoint,
  ];
  return points
    .map((point, index) => `${index === 0 ? "M" : "L"} ${point.x} ${point.y}`)
    .join(" ");
}
