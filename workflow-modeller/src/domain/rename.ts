import type { Step, StepId, WorkflowDefinition } from './types';

/**
 * Return a fresh definition where every occurrence of `oldId` — in the step
 * array, `nextStep`, `conditionalNextSteps` targets, `parallelNextSteps`,
 * `joinStep`, and `boundaryEvents[].targetStepId` — has been replaced with
 * `newId`. Throws if `newId` already exists on another step.
 */
export function renameStepId(
  def: WorkflowDefinition,
  oldId: StepId,
  newId: StepId,
): WorkflowDefinition {
  if (oldId === newId) return def;
  const collision = def.steps.some((s) => s.id === newId);
  if (collision) {
    throw new Error(`Cannot rename "${oldId}" → "${newId}": step id already exists.`);
  }

  const remap = (id: string): string => (id === oldId ? newId : id);

  const steps = def.steps.map((step) => remapStep(step, oldId, newId, remap));
  return { ...def, steps };
}

function remapStep(step: Step, oldId: StepId, newId: StepId, remap: (id: string) => string): Step {
  const base = step.id === oldId ? { ...step, id: newId } : { ...step };

  switch (base.type) {
    case 'SERVICE_TASK':
    case 'USER_TASK': {
      const next: typeof base = {
        ...base,
        ...(base.nextStep !== undefined ? { nextStep: remap(base.nextStep) } : {}),
      };
      return withRemappedBoundary(next, remap);
    }
    case 'WAIT':
      return withRemappedBoundary({ ...base, nextStep: remap(base.nextStep) }, remap);
    case 'TRANSFORMATION':
      return { ...base, nextStep: remap(base.nextStep) };
    case 'JOIN_GATEWAY':
      return { ...base, nextStep: remap(base.nextStep) };
    case 'DECISION': {
      const remapped: Record<string, string> = {};
      for (const [expr, target] of Object.entries(base.conditionalNextSteps)) {
        remapped[expr] = remap(target);
      }
      return { ...base, conditionalNextSteps: remapped };
    }
    case 'PARALLEL_GATEWAY':
      return {
        ...base,
        parallelNextSteps: base.parallelNextSteps.map(remap),
        joinStep: remap(base.joinStep),
      };
    case 'END':
      return base;
  }
}

function withRemappedBoundary<T extends { boundaryEvents?: Array<{ targetStepId: string }> }>(
  step: T,
  remap: (id: string) => string,
): T {
  if (!step.boundaryEvents) return step;
  return {
    ...step,
    boundaryEvents: step.boundaryEvents.map((evt) => ({
      ...evt,
      targetStepId: remap(evt.targetStepId),
    })),
  };
}
