import type { TransformationStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { CommonFields } from './CommonFields';
import { Field, KeyValueList, StepPicker } from './FormPrimitives';

interface TransformationFormProps {
  step: TransformationStep;
}

export function TransformationForm({ step }: TransformationFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);

  const entries = Object.entries(step.transformations).map(([k, v]) => ({
    key: k,
    value: typeof v === 'string' ? v : JSON.stringify(v),
  }));

  function commit(next: Array<{ key: string; value: string }>): void {
    const obj: Record<string, unknown> = {};
    for (const entry of next) {
      if (!entry.key) continue;
      obj[entry.key] = maybeParseLiteral(entry.value);
    }
    updateStepProperty(step.id, 'transformations', obj);
  }

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
      <div className="wm-decision-heading">Transformations</div>
      <p className="wm-field-hint">
        Values wrapped as <code>{'${…}'}</code> are expressions. Other values are literals (JSON).
      </p>
      <KeyValueList
        entries={entries}
        onChange={commit}
        keyLabel="variable"
        valueLabel="expression/literal"
      />
    </>
  );
}

function maybeParseLiteral(raw: string): unknown {
  const trimmed = raw.trim();
  if (trimmed.startsWith('${') && trimmed.endsWith('}')) return raw;
  if (trimmed === 'true') return true;
  if (trimmed === 'false') return false;
  if (trimmed === 'null') return null;
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) return Number(trimmed);
  if (
    (trimmed.startsWith('"') && trimmed.endsWith('"')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']')) ||
    (trimmed.startsWith('{') && trimmed.endsWith('}'))
  ) {
    try {
      return JSON.parse(trimmed);
    } catch {
      return raw;
    }
  }
  return raw;
}
