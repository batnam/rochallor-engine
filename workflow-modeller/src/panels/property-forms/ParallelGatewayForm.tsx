import type { ParallelGatewayStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { CommonFields } from './CommonFields';
import { Field, StepPicker } from './FormPrimitives';

interface ParallelGatewayFormProps {
  step: ParallelGatewayStep;
}

export function ParallelGatewayForm({ step }: ParallelGatewayFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);

  function patchBranch(i: number, value: string): void {
    const next = step.parallelNextSteps.map((s, idx) => (idx === i ? value : s));
    updateStepProperty(step.id, 'parallelNextSteps', next);
  }

  function addBranch(): void {
    updateStepProperty(step.id, 'parallelNextSteps', [...step.parallelNextSteps, '']);
  }

  function removeBranch(i: number): void {
    updateStepProperty(
      step.id,
      'parallelNextSteps',
      step.parallelNextSteps.filter((_, idx) => idx !== i),
    );
  }

  return (
    <>
      <CommonFields step={step} />
      <Field label="Join step" hint="Must reference a JOIN_GATEWAY.">
        <StepPicker
          value={step.joinStep}
          onCommit={(v) => updateStepProperty(step.id, 'joinStep', v)}
          filter={(s) => s.type === 'JOIN_GATEWAY'}
          allowEmpty
        />
      </Field>
      <div className="wm-decision-heading">Parallel branches (≥ 2 required)</div>
      {step.parallelNextSteps.map((target, i) => (
        <div key={`${i}-${target}`} className="wm-parallel-row">
          <StepPicker
            value={target}
            onCommit={(v) => patchBranch(i, v)}
            excludeIds={[step.id]}
            allowEmpty
          />
          <button type="button" onClick={() => removeBranch(i)} aria-label="Remove branch">
            ×
          </button>
        </div>
      ))}
      <button type="button" onClick={addBranch} className="wm-decision-add">
        + add branch
      </button>
    </>
  );
}
