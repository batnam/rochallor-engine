import type { EdgeVariant, GraphEdge, GraphNode, Step, StepId, WorkflowDefinition } from './types';

function edgeId(from: StepId, to: StepId, variant: EdgeVariant, key?: string): string {
  const discriminator =
    variant.kind === 'conditional'
      ? `conditional:${variant.expression}`
      : variant.kind === 'boundary'
        ? `boundary:${variant.index}`
        : variant.kind;
  return `${from}--${discriminator}${key != null ? `:${key}` : ''}-->${to}`;
}

export function toNodes(def: WorkflowDefinition): GraphNode[] {
  return def.steps.map((step, i) => ({
    id: step.id,
    type: step.type,
    name: step.name,
    isEntry: i === 0,
    step,
  }));
}

export function toEdges(def: WorkflowDefinition): GraphEdge[] {
  const edges: GraphEdge[] = [];

  for (const step of def.steps) {
    pushStepEdges(step, edges);
  }

  return edges;
}

function pushStepEdges(step: Step, out: GraphEdge[]): void {
  switch (step.type) {
    case 'SERVICE_TASK':
    case 'USER_TASK':
      if (step.nextStep) {
        out.push(makeEdge(step.id, step.nextStep, { kind: 'sequential' }));
      }
      pushBoundaryEdges(step, out);
      break;

    case 'WAIT':
      out.push(makeEdge(step.id, step.nextStep, { kind: 'sequential' }));
      pushBoundaryEdges(step, out);
      break;

    case 'DECISION':
      for (const [expression, target] of Object.entries(step.conditionalNextSteps)) {
        out.push(makeEdge(step.id, target, { kind: 'conditional', expression }));
      }
      break;

    case 'TRANSFORMATION':
      out.push(makeEdge(step.id, step.nextStep, { kind: 'sequential' }));
      break;

    case 'PARALLEL_GATEWAY':
      for (const target of step.parallelNextSteps) {
        out.push(makeEdge(step.id, target, { kind: 'parallel' }));
      }
      break;

    case 'JOIN_GATEWAY':
      out.push(makeEdge(step.id, step.nextStep, { kind: 'join-out' }));
      break;

    case 'END':
      break;
  }
}

function pushBoundaryEdges(
  step: Extract<Step, { boundaryEvents?: unknown }>,
  out: GraphEdge[],
): void {
  const events = step.boundaryEvents;
  if (!events) return;
  events.forEach((evt, index) => {
    out.push(makeEdge(step.id, evt.targetStepId, { kind: 'boundary', index }));
  });
}

function makeEdge(from: StepId, to: StepId, variant: EdgeVariant): GraphEdge {
  return { id: edgeId(from, to, variant), from, to, variant };
}

/** Build an id → step lookup. */
export function indexSteps(def: WorkflowDefinition): Map<StepId, Step> {
  const map = new Map<StepId, Step>();
  for (const step of def.steps) {
    map.set(step.id, step);
  }
  return map;
}
