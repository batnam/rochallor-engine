import type { JoinGatewayStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { CommonFields } from './CommonFields';
import { Field, StepPicker } from './FormPrimitives';

interface JoinGatewayFormProps {
  step: JoinGatewayStep;
}

export function JoinGatewayForm({ step }: JoinGatewayFormProps): ReactNode {
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
    </>
  );
}
