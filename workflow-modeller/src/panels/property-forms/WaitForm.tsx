import type { WaitStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { BoundaryEventsEditor } from './BoundaryEventsEditor';
import { CommonFields } from './CommonFields';
import { Field, StepPicker } from './FormPrimitives';

interface WaitFormProps {
  step: WaitStep;
}

export function WaitForm({ step }: WaitFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);
  return (
    <>
      <CommonFields step={step} />
      <Field label="Next step">
        <StepPicker
          value={step.nextStep}
          onCommit={(v) => updateStepProperty(step.id, 'nextStep', v)}
          excludeIds={[step.id]}
          allowEmpty
        />
      </Field>
      <BoundaryEventsEditor step={step} events={step.boundaryEvents ?? []} />
    </>
  );
}
