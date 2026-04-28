import type { Step } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useState } from 'react';
import { Field, TextInput } from './FormPrimitives';

interface CommonFieldsProps {
  step: Step;
}

export function CommonFields({ step }: CommonFieldsProps): ReactNode {
  const renameStepId = useWorkflowStore((s) => s.renameStepId);
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);
  const [renameError, setRenameError] = useState<string | null>(null);

  function handleRename(newId: string): void {
    const trimmed = newId.trim();
    if (trimmed === '' || trimmed === step.id) {
      setRenameError(null);
      return;
    }
    try {
      renameStepId(step.id, trimmed);
      useWorkflowStore.getState().select({ kind: 'step', id: trimmed });
      setRenameError(null);
    } catch (e) {
      setRenameError((e as Error).message);
    }
  }

  return (
    <>
      <Field label="Step id" hint="Referenced from other steps — renames cascade.">
        <TextInput value={step.id} onCommit={handleRename} />
      </Field>
      {renameError && <div className="wm-field-error">{renameError}</div>}
      <Field label="Name">
        <TextInput value={step.name} onCommit={(v) => updateStepProperty(step.id, 'name', v)} />
      </Field>
      <Field label="Description">
        <TextInput
          value={step.description ?? ''}
          onCommit={(v) => updateStepProperty(step.id, 'description', v || undefined)}
        />
      </Field>
    </>
  );
}
