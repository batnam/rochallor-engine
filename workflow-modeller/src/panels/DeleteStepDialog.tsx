import type { Step, StepId, WorkflowDefinition } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useMemo, useState } from 'react';
import { StepPicker } from './property-forms/FormPrimitives';

interface DeleteStepDialogProps {
  stepId: StepId | null;
  onClose: () => void;
}

export function DeleteStepDialog({ stepId, onClose }: DeleteStepDialogProps): ReactNode {
  const def = useWorkflowStore((s) => s.definition);
  const deleteStep = useWorkflowStore((s) => s.deleteStep);
  const [replacement, setReplacement] = useState('');

  const references = useMemo(() => (stepId ? findReferences(def, stepId) : []), [def, stepId]);

  if (!stepId) return null;

  function handleDeleteOnly(): void {
    if (!stepId) return;
    deleteStep(stepId);
    onClose();
  }

  function handleReplaceAndDelete(): void {
    if (!stepId || !replacement) return;
    // Manually rewrite references (can't renameStepId to a taken id).
    // Strategy: for each reference site, swap oldId → replacement, then delete the step.
    const rewritten: WorkflowDefinition = {
      ...def,
      steps: def.steps.map((s) => rewriteRefs(s, stepId, replacement)),
    };
    useWorkflowStore.setState({ definition: rewritten, dirty: true });
    deleteStep(stepId);
    onClose();
  }

  return (
    <div className="wm-dialog-backdrop">
      <dialog open className="wm-dialog" aria-labelledby="wm-delete-heading">
        <h2 id="wm-delete-heading">Delete step “{stepId}”</h2>
        {references.length === 0 ? (
          <p className="wm-dialog-hint">No other steps reference this one.</p>
        ) : (
          <>
            <p className="wm-dialog-hint">
              {references.length} reference(s) will be orphaned unless replaced.
            </p>
            <ul className="wm-ref-list">
              {references.map((r, i) => (
                <li key={`${i}-${r}`}>{r}</li>
              ))}
            </ul>
            <div className="wm-field">
              <span className="wm-field-label">Replace references with…</span>
              <StepPicker
                value={replacement}
                onCommit={setReplacement}
                excludeIds={[stepId]}
                allowEmpty
              />
            </div>
          </>
        )}
        <div className="wm-dialog-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          {references.length > 0 && replacement && (
            <button type="button" onClick={handleReplaceAndDelete}>
              Replace &amp; delete
            </button>
          )}
          <button type="button" className="wm-button-danger" onClick={handleDeleteOnly}>
            {references.length > 0 ? 'Delete &amp; scrub refs' : 'Delete'}
          </button>
        </div>
      </dialog>
    </div>
  );
}

function findReferences(def: WorkflowDefinition, id: StepId): string[] {
  const hits: string[] = [];
  for (const step of def.steps) {
    switch (step.type) {
      case 'SERVICE_TASK':
      case 'USER_TASK':
      case 'WAIT':
        if (step.nextStep === id) hits.push(`${step.id}.nextStep`);
        if (
          (step.type === 'SERVICE_TASK' || step.type === 'USER_TASK' || step.type === 'WAIT') &&
          step.boundaryEvents
        ) {
          step.boundaryEvents.forEach((evt, i) => {
            if (evt.targetStepId === id) {
              hits.push(`${step.id}.boundaryEvents[${i}].targetStepId`);
            }
          });
        }
        break;
      case 'TRANSFORMATION':
      case 'JOIN_GATEWAY':
        if (step.nextStep === id) hits.push(`${step.id}.nextStep`);
        break;
      case 'DECISION':
        for (const [expr, target] of Object.entries(step.conditionalNextSteps)) {
          if (target === id) hits.push(`${step.id}.conditionalNextSteps["${expr}"]`);
        }
        break;
      case 'PARALLEL_GATEWAY':
        step.parallelNextSteps.forEach((t, i) => {
          if (t === id) hits.push(`${step.id}.parallelNextSteps[${i}]`);
        });
        if (step.joinStep === id) hits.push(`${step.id}.joinStep`);
        break;
      case 'END':
        break;
    }
  }
  return hits;
}

function rewriteRefs(step: Step, oldId: StepId, newId: StepId): Step {
  switch (step.type) {
    case 'SERVICE_TASK':
    case 'USER_TASK': {
      let s = step;
      if (s.nextStep === oldId) s = { ...s, nextStep: newId };
      if (s.boundaryEvents) {
        s = {
          ...s,
          boundaryEvents: s.boundaryEvents.map((e) =>
            e.targetStepId === oldId ? { ...e, targetStepId: newId } : e,
          ),
        };
      }
      return s;
    }
    case 'WAIT': {
      let s = step;
      if (s.nextStep === oldId) s = { ...s, nextStep: newId };
      if (s.boundaryEvents) {
        s = {
          ...s,
          boundaryEvents: s.boundaryEvents.map((e) =>
            e.targetStepId === oldId ? { ...e, targetStepId: newId } : e,
          ),
        };
      }
      return s;
    }
    case 'TRANSFORMATION':
      return step.nextStep === oldId ? { ...step, nextStep: newId } : step;
    case 'JOIN_GATEWAY':
      return step.nextStep === oldId ? { ...step, nextStep: newId } : step;
    case 'DECISION': {
      const next: Record<string, string> = {};
      for (const [expr, target] of Object.entries(step.conditionalNextSteps)) {
        next[expr] = target === oldId ? newId : target;
      }
      return { ...step, conditionalNextSteps: next };
    }
    case 'PARALLEL_GATEWAY':
      return {
        ...step,
        parallelNextSteps: step.parallelNextSteps.map((t) => (t === oldId ? newId : t)),
        joinStep: step.joinStep === oldId ? newId : step.joinStep,
      };
    case 'END':
      return step;
  }
}
