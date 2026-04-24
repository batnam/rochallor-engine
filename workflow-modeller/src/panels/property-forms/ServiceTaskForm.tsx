import type { ServiceTaskStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { BoundaryEventsEditor } from './BoundaryEventsEditor';
import { CommonFields } from './CommonFields';
import { Field, NumberInput, StepPicker, TextInput } from './FormPrimitives';

interface ServiceTaskFormProps {
  step: ServiceTaskStep;
}

export function ServiceTaskForm({ step }: ServiceTaskFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);
  return (
    <>
      <CommonFields step={step} />
      <Field label="Job type">
        <TextInput
          value={step.jobType ?? ''}
          onCommit={(v) => updateStepProperty(step.id, 'jobType', v || undefined)}
        />
      </Field>
      <Field label="Retry count">
        <NumberInput
          value={step.retryCount}
          onCommit={(v) => updateStepProperty(step.id, 'retryCount', v)}
          min={0}
        />
      </Field>
      <Field label="Next step">
        <StepPicker
          value={step.nextStep ?? ''}
          onCommit={(v) => updateStepProperty(step.id, 'nextStep', v || undefined)}
          excludeIds={[step.id]}
          allowEmpty
        />
      </Field>
      <BoundaryEventsEditor step={step} events={step.boundaryEvents ?? []} />
    </>
  );
}
