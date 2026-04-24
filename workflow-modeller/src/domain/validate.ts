import { lintDecisionExpression, lintTransformationExpression } from './expression/lint';
import { indexSteps } from './graph';
import { KNOWN_ROOT_KEYS, KNOWN_STEP_KEYS, STEP_TYPES } from './schema';
import type { Diagnostic, Step, StepId, WorkflowDefinition } from './types';

const ID_PATTERN = /^[A-Za-z0-9_:\-]+$/;
const ISO_DURATION_PATTERN =
  /^P(?!$)(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(?=\d)(\d+H)?(\d+M)?(\d+(?:\.\d+)?S)?)?$/;

export function validate(def: WorkflowDefinition): Diagnostic[] {
  const out: Diagnostic[] = [];
  const stepIndex = indexSteps(def);

  checkIdFormat(def, out);
  checkNameRequired(def, out);
  checkStepsNonEmpty(def, out);
  checkStepIdsUnique(def, out);
  checkStepIdsPresent(def, out);
  checkStepTypesValid(def, out);
  checkNextWorkflowConsistency(def, out);

  for (const step of def.steps) {
    checkStepVariantShape(step, out);
    checkRefsResolve(step, stepIndex, out);
    checkBoundaryEvents(step, stepIndex, out);
    checkExpressions(step, def, out);
  }

  checkParallelJoinTarget(def, stepIndex, out);
  checkReachability(def, stepIndex, out);
  checkNestedParallel(def, stepIndex, out);
  checkGraphCycle(def, stepIndex, out);
  checkUnknownFields(def, out);

  return out;
}

function checkIdFormat(def: WorkflowDefinition, out: Diagnostic[]): void {
  if (!def.id || !ID_PATTERN.test(def.id) || def.id.length > 256) {
    out.push({
      code: 'ID_FORMAT',
      severity: 'error',
      message: 'Workflow id must match [A-Za-z0-9_:-]+ and be ≤ 256 characters.',
      field: 'id',
    });
  }
}

function checkNameRequired(def: WorkflowDefinition, out: Diagnostic[]): void {
  if (!def.name || def.name.trim() === '') {
    out.push({
      code: 'NAME_REQUIRED',
      severity: 'error',
      message: 'Workflow name is required.',
      field: 'name',
    });
  }
  for (const step of def.steps) {
    if (!step.name || step.name.trim() === '') {
      out.push({
        code: 'NAME_REQUIRED',
        severity: 'error',
        message: `Step "${step.id}" must have a name.`,
        nodeId: step.id,
        field: 'name',
      });
    }
  }
}

function checkStepsNonEmpty(def: WorkflowDefinition, out: Diagnostic[]): void {
  if (def.steps.length === 0) {
    out.push({
      code: 'STEPS_NONEMPTY',
      severity: 'error',
      message: 'Workflow must contain at least one step.',
      field: 'steps',
    });
  }
}

function checkStepIdsUnique(def: WorkflowDefinition, out: Diagnostic[]): void {
  const seen = new Set<StepId>();
  for (const step of def.steps) {
    if (seen.has(step.id)) {
      out.push({
        code: 'STEP_ID_UNIQUE',
        severity: 'error',
        message: `Duplicate step id: "${step.id}".`,
        nodeId: step.id,
        field: 'id',
      });
    }
    seen.add(step.id);
  }
}

function checkStepIdsPresent(def: WorkflowDefinition, out: Diagnostic[]): void {
  def.steps.forEach((step, i) => {
    if (!step.id || step.id.trim() === '') {
      out.push({
        code: 'STEP_ID_REQUIRED',
        severity: 'error',
        message: `Step at index ${i} is missing an id.`,
        field: 'id',
      });
    }
  });
}

function checkStepTypesValid(def: WorkflowDefinition, out: Diagnostic[]): void {
  const valid = new Set<string>(STEP_TYPES);
  for (const step of def.steps) {
    if (!valid.has(step.type)) {
      out.push({
        code: 'STEP_TYPE_VALID',
        severity: 'error',
        message: `Step "${step.id}" has unknown type "${step.type}".`,
        nodeId: step.id,
        field: 'type',
      });
    }
  }
}

function checkNextWorkflowConsistency(def: WorkflowDefinition, out: Diagnostic[]): void {
  const enabled = def.autoStartNextWorkflow === true;
  const hasId = typeof def.nextWorkflowId === 'string' && def.nextWorkflowId.trim() !== '';
  if (enabled !== hasId) {
    out.push({
      code: 'NEXT_WORKFLOW_CONSISTENCY',
      severity: 'error',
      message: 'autoStartNextWorkflow must be set together with a non-empty nextWorkflowId.',
      field: 'nextWorkflowId',
    });
  }
}

function checkStepVariantShape(step: Step, out: Diagnostic[]): void {
  switch (step.type) {
    case 'DECISION':
      if (Object.keys(step.conditionalNextSteps).length === 0) {
        out.push({
          code: 'DECISION_HAS_BRANCHES',
          severity: 'error',
          message: `DECISION "${step.id}" must declare at least one branch.`,
          nodeId: step.id,
          field: 'conditionalNextSteps',
        });
      }
      break;
    case 'TRANSFORMATION':
      if (!step.nextStep || step.nextStep.trim() === '') {
        out.push({
          code: 'TRANSFORMATION_HAS_NEXT',
          severity: 'error',
          message: `TRANSFORMATION "${step.id}" must declare nextStep.`,
          nodeId: step.id,
          field: 'nextStep',
        });
      }
      if (Object.keys(step.transformations).length === 0) {
        out.push({
          code: 'TRANSFORMATION_HAS_ENTRIES',
          severity: 'error',
          message: `TRANSFORMATION "${step.id}" must declare at least one transformation entry.`,
          nodeId: step.id,
          field: 'transformations',
        });
      }
      break;
    case 'WAIT':
      if (!step.nextStep || step.nextStep.trim() === '') {
        out.push({
          code: 'WAIT_HAS_NEXT',
          severity: 'error',
          message: `WAIT "${step.id}" must declare nextStep.`,
          nodeId: step.id,
          field: 'nextStep',
        });
      }
      break;
    case 'PARALLEL_GATEWAY':
      if (step.parallelNextSteps.length < 2) {
        out.push({
          code: 'PARALLEL_MIN_BRANCHES',
          severity: 'error',
          message: `PARALLEL_GATEWAY "${step.id}" must declare at least two parallel branches.`,
          nodeId: step.id,
          field: 'parallelNextSteps',
        });
      }
      if (!step.joinStep || step.joinStep.trim() === '') {
        out.push({
          code: 'PARALLEL_HAS_JOIN',
          severity: 'error',
          message: `PARALLEL_GATEWAY "${step.id}" must declare a joinStep.`,
          nodeId: step.id,
          field: 'joinStep',
        });
      }
      break;
    case 'JOIN_GATEWAY':
      if (!step.nextStep || step.nextStep.trim() === '') {
        out.push({
          code: 'JOIN_HAS_NEXT',
          severity: 'error',
          message: `JOIN_GATEWAY "${step.id}" must declare nextStep.`,
          nodeId: step.id,
          field: 'nextStep',
        });
      }
      break;
    case 'SERVICE_TASK':
    case 'USER_TASK':
    case 'END':
      break;
  }
}

function checkRefsResolve(step: Step, index: Map<StepId, Step>, out: Diagnostic[]): void {
  const missing = (target: string, field: string, branchKey?: string): void => {
    out.push({
      code: 'REF_RESOLVES',
      severity: 'error',
      message: `Step "${step.id}" references missing step "${target}".`,
      nodeId: step.id,
      field,
      ...(branchKey !== undefined ? { branchKey } : {}),
    });
  };

  switch (step.type) {
    case 'SERVICE_TASK':
    case 'USER_TASK':
      if (step.nextStep && !index.has(step.nextStep)) missing(step.nextStep, 'nextStep');
      break;
    case 'WAIT':
    case 'TRANSFORMATION':
    case 'JOIN_GATEWAY':
      if (step.nextStep && !index.has(step.nextStep)) missing(step.nextStep, 'nextStep');
      break;
    case 'DECISION':
      for (const [expr, target] of Object.entries(step.conditionalNextSteps)) {
        if (!index.has(target)) missing(target, 'conditionalNextSteps', expr);
      }
      break;
    case 'PARALLEL_GATEWAY':
      for (const target of step.parallelNextSteps) {
        if (!index.has(target)) missing(target, 'parallelNextSteps');
      }
      if (step.joinStep && !index.has(step.joinStep)) missing(step.joinStep, 'joinStep');
      break;
    case 'END':
      break;
  }
}

function checkBoundaryEvents(step: Step, index: Map<StepId, Step>, out: Diagnostic[]): void {
  const supports =
    step.type === 'SERVICE_TASK' || step.type === 'USER_TASK' || step.type === 'WAIT';
  if (!supports) {
    // Passthrough may still carry boundaryEvents on other variants; flag it.
    if (Array.isArray((step as Record<string, unknown>).boundaryEvents)) {
      out.push({
        code: 'BOUNDARY_PARENT_SUPPORTS',
        severity: 'error',
        message: `Step "${step.id}" of type ${step.type} cannot have boundary events.`,
        nodeId: step.id,
        field: 'boundaryEvents',
      });
    }
    return;
  }

  const events = step.boundaryEvents;
  if (!events) return;

  events.forEach((evt, i) => {
    if (evt.type !== 'TIMER') {
      out.push({
        code: 'BOUNDARY_TYPE',
        severity: 'error',
        message: `Boundary event ${i} on "${step.id}" must be of type TIMER.`,
        nodeId: step.id,
        field: 'boundaryEvents',
        boundaryIndex: i,
      });
    }
    if (!evt.duration || !ISO_DURATION_PATTERN.test(evt.duration)) {
      out.push({
        code: 'BOUNDARY_DURATION',
        severity: 'error',
        message: `Boundary event ${i} on "${step.id}" must have a non-empty ISO-8601 duration.`,
        nodeId: step.id,
        field: 'boundaryEvents',
        boundaryIndex: i,
      });
    }
    if (!evt.targetStepId || !index.has(evt.targetStepId)) {
      out.push({
        code: 'BOUNDARY_TARGET_RESOLVES',
        severity: 'error',
        message: `Boundary event ${i} on "${step.id}" references missing step "${evt.targetStepId}".`,
        nodeId: step.id,
        field: 'boundaryEvents',
        boundaryIndex: i,
      });
    }
  });
}

function checkExpressions(step: Step, def: WorkflowDefinition, out: Diagnostic[]): void {
  if (step.type === 'DECISION') {
    const knownVars = collectKnownVariables(def);
    for (const expr of Object.keys(step.conditionalNextSteps)) {
      out.push(
        ...lintDecisionExpression(expr, knownVars, {
          nodeId: step.id,
          field: 'conditionalNextSteps',
          branchKey: expr,
        }),
      );
    }
  } else if (step.type === 'TRANSFORMATION') {
    for (const [key, value] of Object.entries(step.transformations)) {
      if (typeof value === 'string') {
        out.push(
          ...lintTransformationExpression(value, {
            nodeId: step.id,
            field: 'transformations',
            branchKey: key,
          }),
        );
      }
    }
  }
}

function collectKnownVariables(def: WorkflowDefinition): Set<string> {
  const vars = new Set<string>();
  for (const step of def.steps) {
    if (step.type === 'TRANSFORMATION') {
      for (const key of Object.keys(step.transformations)) {
        vars.add(key);
      }
    }
  }
  return vars;
}

function checkParallelJoinTarget(
  def: WorkflowDefinition,
  index: Map<StepId, Step>,
  out: Diagnostic[],
): void {
  for (const step of def.steps) {
    if (step.type !== 'PARALLEL_GATEWAY') continue;
    const join = index.get(step.joinStep);
    if (!join) continue;
    if (join.type !== 'JOIN_GATEWAY') {
      out.push({
        code: 'PARALLEL_HAS_JOIN',
        severity: 'error',
        message: `PARALLEL_GATEWAY "${step.id}" must reference a JOIN_GATEWAY (got ${join.type}).`,
        nodeId: step.id,
        field: 'joinStep',
      });
    }
  }
}

function outgoingTargets(step: Step): StepId[] {
  switch (step.type) {
    case 'SERVICE_TASK':
    case 'USER_TASK':
      return step.nextStep ? [step.nextStep] : [];
    case 'WAIT':
    case 'TRANSFORMATION':
    case 'JOIN_GATEWAY':
      return [step.nextStep];
    case 'DECISION':
      return Object.values(step.conditionalNextSteps);
    case 'PARALLEL_GATEWAY':
      return [...step.parallelNextSteps];
    case 'END':
      return [];
  }
}

function allOutgoing(step: Step): StepId[] {
  const refs = outgoingTargets(step);
  if (step.type === 'SERVICE_TASK' || step.type === 'USER_TASK' || step.type === 'WAIT') {
    const events = step.boundaryEvents;
    if (events) {
      for (const evt of events) refs.push(evt.targetStepId);
    }
  }
  return refs;
}

function checkReachability(
  def: WorkflowDefinition,
  index: Map<StepId, Step>,
  out: Diagnostic[],
): void {
  if (def.steps.length === 0) return;
  const entry = def.steps[0];
  if (!entry) return;

  const visited = new Set<StepId>();
  const stack: StepId[] = [entry.id];
  while (stack.length > 0) {
    const id = stack.pop();
    if (!id || visited.has(id)) continue;
    visited.add(id);
    const step = index.get(id);
    if (!step) continue;
    for (const target of allOutgoing(step)) {
      if (!visited.has(target)) stack.push(target);
    }
  }

  for (const step of def.steps) {
    if (!visited.has(step.id)) {
      out.push({
        code: 'ALL_REACHABLE',
        severity: 'error',
        message: `Step "${step.id}" is not reachable from the entry step.`,
        nodeId: step.id,
      });
    }
  }

  let endReached = false;
  for (const id of visited) {
    if (index.get(id)?.type === 'END') {
      endReached = true;
      break;
    }
  }
  if (!endReached) {
    out.push({
      code: 'END_REACHABLE',
      severity: 'error',
      message: 'No END step is reachable from the entry step.',
    });
  }
}

function checkNestedParallel(
  def: WorkflowDefinition,
  index: Map<StepId, Step>,
  out: Diagnostic[],
): void {
  for (const step of def.steps) {
    if (step.type !== 'PARALLEL_GATEWAY') continue;
    const visited = new Set<StepId>();
    const stack: StepId[] = [...step.parallelNextSteps];
    while (stack.length > 0) {
      const id = stack.pop();
      if (!id || visited.has(id)) continue;
      if (id === step.joinStep) continue;
      visited.add(id);
      const s = index.get(id);
      if (!s) continue;
      if (s.type === 'PARALLEL_GATEWAY') {
        out.push({
          code: 'NO_NESTED_PARALLEL',
          severity: 'error',
          message: `PARALLEL_GATEWAY "${s.id}" is nested inside "${step.id}" without first reaching its join.`,
          nodeId: s.id,
        });
      }
      for (const target of outgoingTargets(s)) {
        if (!visited.has(target)) stack.push(target);
      }
    }
  }
}

function checkGraphCycle(
  def: WorkflowDefinition,
  index: Map<StepId, Step>,
  out: Diagnostic[],
): void {
  const color = new Map<StepId, 'white' | 'gray' | 'black'>();
  for (const step of def.steps) color.set(step.id, 'white');

  const onCycle: StepId[] = [];
  const dfs = (id: StepId): void => {
    if (onCycle.length > 0) return;
    color.set(id, 'gray');
    const step = index.get(id);
    if (step) {
      for (const target of allOutgoing(step)) {
        const c = color.get(target);
        if (c === 'gray') {
          onCycle.push(target);
          return;
        }
        if (c === 'white') dfs(target);
      }
    }
    color.set(id, 'black');
  };

  for (const step of def.steps) {
    if (color.get(step.id) === 'white') dfs(step.id);
    if (onCycle.length > 0) break;
  }

  if (onCycle.length > 0) {
    const node = onCycle[0];
    out.push({
      code: 'GRAPH_CYCLE',
      severity: 'warning',
      message: `Cycle detected in step graph (involves "${node}").`,
      ...(node !== undefined ? { nodeId: node } : {}),
    });
  }
}

function checkUnknownFields(def: WorkflowDefinition, out: Diagnostic[]): void {
  const rootUnknown = Object.keys(def).filter(
    (k) => !(KNOWN_ROOT_KEYS as readonly string[]).includes(k),
  );
  if (rootUnknown.length > 0) {
    out.push({
      code: 'UNKNOWN_FIELDS_PRESENT',
      severity: 'warning',
      message: `Unknown top-level field(s): ${rootUnknown.join(', ')}.`,
    });
  }
  for (const step of def.steps) {
    const known = KNOWN_STEP_KEYS[step.type] ?? [];
    const extras = Object.keys(step).filter((k) => !known.includes(k));
    if (extras.length > 0) {
      out.push({
        code: 'UNKNOWN_FIELDS_PRESENT',
        severity: 'warning',
        message: `Step "${step.id}" has unknown field(s): ${extras.join(', ')}.`,
        nodeId: step.id,
      });
    }
  }
}
