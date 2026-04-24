import type { BoundaryEvent, Step } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { Field, StepPicker, TextInput } from './FormPrimitives';

interface BoundaryEventsEditorProps {
  step: Step;
  events: BoundaryEvent[];
}

export function BoundaryEventsEditor({ step, events }: BoundaryEventsEditorProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);

  function commit(next: BoundaryEvent[]): void {
    updateStepProperty(step.id, 'boundaryEvents', next.length === 0 ? undefined : next);
  }

  function patch(i: number, partial: Partial<BoundaryEvent>): void {
    commit(events.map((e, idx) => (idx === i ? { ...e, ...partial } : e)));
  }

  function remove(i: number): void {
    commit(events.filter((_, idx) => idx !== i));
  }

  function add(): void {
    commit([...events, { type: 'TIMER', duration: 'PT1H', interrupting: false, targetStepId: '' }]);
  }

  return (
    <div className="wm-boundary-editor">
      <div className="wm-boundary-heading">Boundary events (timers)</div>
      {events.length === 0 && (
        <p className="wm-field-hint">None. Add a TIMER to trigger a side-path on timeout.</p>
      )}
      {events.map((evt, i) => (
        <div key={`${i}-${evt.targetStepId}-${evt.duration}`} className="wm-boundary-row">
          <div className="wm-field-row">
            <Field label="Duration">
              <TextInput value={evt.duration} onCommit={(v) => patch(i, { duration: v })} />
            </Field>
            <Field label="Target">
              <StepPicker
                value={evt.targetStepId}
                onCommit={(v) => patch(i, { targetStepId: v })}
                excludeIds={[step.id]}
                allowEmpty
              />
            </Field>
            <div className="wm-field wm-field--inline">
              <input
                id={`boundary-interrupting-${step.id}-${i}`}
                type="checkbox"
                checked={evt.interrupting}
                onChange={(e) => patch(i, { interrupting: e.target.checked })}
              />
              <label htmlFor={`boundary-interrupting-${step.id}-${i}`}>Interrupting</label>
            </div>
          </div>
          <button type="button" onClick={() => remove(i)} className="wm-boundary-remove">
            Remove
          </button>
        </div>
      ))}
      <button type="button" onClick={add} className="wm-boundary-add">
        + add timer
      </button>
    </div>
  );
}
