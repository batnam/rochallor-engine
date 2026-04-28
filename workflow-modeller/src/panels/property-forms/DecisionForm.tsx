import { lintDecisionExpression } from '@/domain/expression/lint';
import type { DecisionStep } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useMemo } from 'react';
import { CommonFields } from './CommonFields';
import { StepPicker } from './FormPrimitives';

interface DecisionFormProps {
  step: DecisionStep;
}

export function DecisionForm({ step }: DecisionFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);
  const allSteps = useWorkflowStore((s) => s.definition.steps);
  const entries = Object.entries(step.conditionalNextSteps);

  const knownVars = useMemo(() => {
    const set = new Set<string>();
    for (const s of allSteps) {
      if (s.type === 'TRANSFORMATION') {
        for (const k of Object.keys(s.transformations)) set.add(k);
      }
    }
    return set;
  }, [allSteps]);

  function commit(next: Array<[string, string]>): void {
    updateStepProperty(step.id, 'conditionalNextSteps', Object.fromEntries(next));
  }

  function patchKey(i: number, key: string): void {
    const next: Array<[string, string]> = entries.map(([k, v], idx) =>
      idx === i ? [key, v] : [k, v],
    );
    commit(next);
  }

  function patchValue(i: number, value: string): void {
    const next: Array<[string, string]> = entries.map(([k, v], idx) =>
      idx === i ? [k, value] : [k, v],
    );
    commit(next);
  }

  function remove(i: number): void {
    commit(entries.filter((_, idx) => idx !== i));
  }

  function move(i: number, dir: -1 | 1): void {
    const target = i + dir;
    if (target < 0 || target >= entries.length) return;
    const next = [...entries];
    const a = next[i];
    const b = next[target];
    if (!a || !b) return;
    next[target] = a;
    next[i] = b;
    commit(next);
  }

  function add(): void {
    commit([...entries, ['', '']]);
  }

  return (
    <>
      <CommonFields step={step} />
      <div className="wm-decision-heading">Branches (order matters)</div>
      {entries.length === 0 && <p className="wm-field-hint">No branches yet. Add one below.</p>}
      {entries.map(([expr, target], i) => {
        const lint = expr ? lintDecisionExpression(expr, knownVars, {}) : [];
        const error = lint.find((d) => d.severity === 'error');
        const warning = lint.find((d) => d.severity === 'warning');
        return (
          <div key={`${i}-${expr}`}>
            <div className="wm-decision-row">
              <input
                type="text"
                className="wm-input wm-decision-expr"
                defaultValue={expr}
                placeholder="boolean expression (e.g. score >= 650)"
                onBlur={(e) => patchKey(i, e.target.value)}
              />
              <StepPicker
                value={target}
                onCommit={(v) => patchValue(i, v)}
                excludeIds={[step.id]}
                allowEmpty
              />
              <button
                type="button"
                onClick={() => move(i, -1)}
                disabled={i === 0}
                aria-label="Move up"
              >
                ↑
              </button>
              <button
                type="button"
                onClick={() => move(i, 1)}
                disabled={i === entries.length - 1}
                aria-label="Move down"
              >
                ↓
              </button>
              <button type="button" onClick={() => remove(i)} aria-label="Remove branch">
                ×
              </button>
            </div>
            {error && <div className="wm-field-error">{error.message}</div>}
            {!error && warning && <div className="wm-field-warning">{warning.message}</div>}
          </div>
        );
      })}
      <button type="button" onClick={add} className="wm-decision-add">
        + add branch
      </button>
    </>
  );
}
