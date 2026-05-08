import type { DecisionTableStep, HitPolicy } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { CommonFields } from './CommonFields';
import { Field, StepPicker } from './FormPrimitives';

interface DecisionTableFormProps {
  step: DecisionTableStep;
}

type Rule = DecisionTableStep['decisionTable']['rules'][number];

const HIT_POLICY_OPTIONS: ReadonlyArray<{ value: HitPolicy; label: string; hint: string }> = [
  {
    value: 'U',
    label: 'Unique',
    hint: 'At most one rule may match; multiple matches fail the step.',
  },
  { value: 'F', label: 'First', hint: 'First matching rule in document order wins.' },
  {
    value: 'A',
    label: 'Any',
    hint: 'Every matching rule must produce structurally-equal outputs.',
  },
  {
    value: 'R',
    label: 'Rule Order',
    hint: 'Every matching rule contributes; per-column lists in document order.',
  },
  {
    value: 'C',
    label: 'Collect',
    hint: 'Every matching rule contributes; per-column lists (no order promised).',
  },
  { value: 'C+', label: 'Collect (Sum)', hint: 'Sum each output column across matching rules.' },
  {
    value: 'C#',
    label: 'Collect (Count)',
    hint: 'Count of matching rules as the scalar for every output column.',
  },
  { value: 'C>', label: 'Collect (Max)', hint: 'Max of each output column across matching rules.' },
  { value: 'C<', label: 'Collect (Min)', hint: 'Min of each output column across matching rules.' },
];

export function DecisionTableForm({ step }: DecisionTableFormProps): ReactNode {
  const updateStepProperty = useWorkflowStore((s) => s.updateStepProperty);
  const rules = step.decisionTable.rules;

  const inputColumns = collectInputColumns(rules);
  const outputColumns = collectOutputColumns(rules);

  const hitPolicy: HitPolicy = step.hitPolicy ?? 'U';

  function commitRules(next: Rule[]): void {
    updateStepProperty(step.id, 'decisionTable', {
      ...step.decisionTable,
      rules: next,
    });
  }

  function commitHitPolicy(value: HitPolicy): void {
    updateStepProperty(step.id, 'hitPolicy', value);
  }

  function commitNextStep(value: string): void {
    updateStepProperty(step.id, 'nextStep', value);
  }

  function patchRule(i: number, patch: Partial<Rule>): void {
    commitRules(rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }

  function patchCell(i: number, column: string, expr: string): void {
    const rule = rules[i];
    if (!rule) return;
    const when = { ...rule.when };
    if (expr === '') {
      delete when[column];
    } else {
      when[column] = expr;
    }
    patchRule(i, { when });
  }

  function patchOutputCell(i: number, column: string, raw: string): void {
    const rule = rules[i];
    if (!rule) return;
    const outputs = { ...rule.outputs };
    if (raw === '') {
      delete outputs[column];
    } else {
      outputs[column] = parseOutputValue(raw);
    }
    patchRule(i, { outputs });
  }

  function move(i: number, dir: -1 | 1): void {
    const target = i + dir;
    if (target < 0 || target >= rules.length) return;
    const next = [...rules];
    const a = next[i];
    const b = next[target];
    if (!a || !b) return;
    next[target] = a;
    next[i] = b;
    commitRules(next);
  }

  function remove(i: number): void {
    commitRules(rules.filter((_, idx) => idx !== i));
  }

  function addRule(): void {
    const when: Record<string, string> = {};
    for (const col of inputColumns) when[col] = '';
    const outputs: Record<string, unknown> = {};
    for (const col of outputColumns) outputs[col] = '';
    commitRules([...rules, { when, outputs }]);
  }

  function addInputColumn(): void {
    const name = window.prompt('Input column name (a variable name):');
    if (!name) return;
    if (inputColumns.includes(name)) {
      window.alert(`Column "${name}" already exists.`);
      return;
    }
    commitRules(rules.map((r) => ({ ...r, when: { ...r.when, [name]: '' } })));
  }

  function renameInputColumn(oldName: string): void {
    const newName = window.prompt(`Rename input column "${oldName}" to:`, oldName);
    if (!newName || newName === oldName) return;
    if (inputColumns.includes(newName)) {
      window.alert(`Column "${newName}" already exists.`);
      return;
    }
    commitRules(
      rules.map((r) => {
        if (!(oldName in r.when)) return r;
        const when: Record<string, string> = {};
        for (const [k, v] of Object.entries(r.when)) {
          when[k === oldName ? newName : k] = v;
        }
        return { ...r, when };
      }),
    );
  }

  function removeInputColumn(name: string): void {
    commitRules(
      rules.map((r) => {
        if (!(name in r.when)) return r;
        const { [name]: _, ...rest } = r.when;
        return { ...r, when: rest };
      }),
    );
  }

  function addOutputColumn(): void {
    const name = window.prompt('Output variable name:');
    if (!name) return;
    if (outputColumns.includes(name)) {
      window.alert(`Output "${name}" already exists.`);
      return;
    }
    if (rules.length === 0) return;
    commitRules(rules.map((r) => ({ ...r, outputs: { ...r.outputs, [name]: '' } })));
  }

  function renameOutputColumn(oldName: string): void {
    const newName = window.prompt(`Rename output "${oldName}" to:`, oldName);
    if (!newName || newName === oldName) return;
    if (outputColumns.includes(newName)) {
      window.alert(`Output "${newName}" already exists.`);
      return;
    }
    commitRules(
      rules.map((r) => {
        if (!(oldName in r.outputs)) return r;
        const outputs: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(r.outputs)) {
          outputs[k === oldName ? newName : k] = v;
        }
        return { ...r, outputs };
      }),
    );
  }

  function removeOutputColumn(name: string): void {
    commitRules(
      rules.map((r) => {
        if (!(name in r.outputs)) return r;
        const { [name]: _, ...rest } = r.outputs;
        return { ...r, outputs: rest };
      }),
    );
  }

  const policyOption = HIT_POLICY_OPTIONS.find((p) => p.value === hitPolicy);

  return (
    <>
      <CommonFields step={step} />

      <Field label="Hit policy" hint={policyOption?.hint}>
        <select
          aria-label="Hit policy"
          className="wm-input"
          value={hitPolicy}
          onChange={(e) => commitHitPolicy(e.target.value as HitPolicy)}
        >
          {HIT_POLICY_OPTIONS.map((p) => (
            <option key={p.value} value={p.value}>
              {p.value} — {p.label}
            </option>
          ))}
        </select>
      </Field>

      <Field
        label="Next step"
        hint="The single step the workflow advances to after the table runs."
      >
        <StepPicker
          value={step.nextStep ?? ''}
          onCommit={commitNextStep}
          excludeIds={[step.id]}
          allowEmpty
          ariaLabel="Next step"
        />
      </Field>

      <div className="wm-decision-heading">Inputs</div>
      <div className="wm-dt-columns">
        {inputColumns.length === 0 && (
          <span className="wm-field-hint">No input columns. Add one below.</span>
        )}
        {inputColumns.map((col) => (
          <span key={`in-${col}`} className="wm-dt-column-chip">
            {col}
            <button
              type="button"
              onClick={() => renameInputColumn(col)}
              aria-label={`Rename ${col}`}
            >
              ✎
            </button>
            <button
              type="button"
              onClick={() => removeInputColumn(col)}
              aria-label={`Remove ${col}`}
            >
              ×
            </button>
          </span>
        ))}
        <button type="button" onClick={addInputColumn} className="wm-dt-add-col">
          + add input
        </button>
      </div>

      <div className="wm-decision-heading">Outputs</div>
      <div className="wm-dt-columns">
        {outputColumns.length === 0 && (
          <span className="wm-field-hint">No output columns. Add one to assign variables.</span>
        )}
        {outputColumns.map((col) => (
          <span key={`out-${col}`} className="wm-dt-column-chip">
            {col}
            <button
              type="button"
              onClick={() => renameOutputColumn(col)}
              aria-label={`Rename ${col}`}
            >
              ✎
            </button>
            <button
              type="button"
              onClick={() => removeOutputColumn(col)}
              aria-label={`Remove ${col}`}
            >
              ×
            </button>
          </span>
        ))}
        <button
          type="button"
          onClick={addOutputColumn}
          className="wm-dt-add-col"
          disabled={rules.length === 0}
        >
          + add output
        </button>
      </div>

      <div className="wm-decision-heading">Rules</div>
      {rules.length === 0 && <p className="wm-field-hint">No rules yet. Add one below.</p>}
      {rules.map((rule, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rule order is the wire identity — there is no other stable id, and reorders intentionally re-render the row.
        <div key={`rule-${i}`} className="wm-dt-rule-row wm-dt-rule-card">
          <div className="wm-dt-rule-card-header">
            <span className="wm-dt-rule-card-title">Rule {i + 1}</span>
            <div className="wm-dt-rule-card-actions">
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
                disabled={i === rules.length - 1}
                aria-label="Move down"
              >
                ↓
              </button>
              <button type="button" onClick={() => remove(i)} aria-label="Remove rule">
                ×
              </button>
            </div>
          </div>

          {inputColumns.length > 0 && (
            <div className="wm-dt-rule-section wm-dt-rule-section--when">
              <div className="wm-dt-rule-section-label">When</div>
              {inputColumns.map((col) => (
                <label key={`in-${i}-${col}`} className="wm-dt-rule-cell-field">
                  <span className="wm-dt-rule-cell-name">{col}</span>
                  <input
                    type="text"
                    className="wm-input wm-dt-cell"
                    defaultValue={rule.when[col] ?? ''}
                    placeholder="boolean expr, blank = *"
                    onBlur={(e) => {
                      if ((rule.when[col] ?? '') !== e.target.value) {
                        patchCell(i, col, e.target.value);
                      }
                    }}
                  />
                </label>
              ))}
            </div>
          )}

          {outputColumns.length > 0 && (
            <div className="wm-dt-rule-section wm-dt-rule-section--then">
              <div className="wm-dt-rule-section-label">Then</div>
              {outputColumns.map((col) => (
                <label key={`out-${i}-${col}`} className="wm-dt-rule-cell-field">
                  <span className="wm-dt-rule-cell-name">{col}</span>
                  <input
                    type="text"
                    className="wm-input wm-dt-cell wm-dt-cell--output"
                    defaultValue={renderOutputValue(rule.outputs[col])}
                    placeholder="literal or ${expr}"
                    onBlur={(e) => {
                      const current = renderOutputValue(rule.outputs[col]);
                      if (current !== e.target.value) {
                        patchOutputCell(i, col, e.target.value);
                      }
                    }}
                  />
                </label>
              ))}
            </div>
          )}
        </div>
      ))}
      <button type="button" onClick={addRule} className="wm-decision-add">
        + add rule
      </button>
    </>
  );
}

function collectInputColumns(rules: Rule[]): string[] {
  const seen: string[] = [];
  for (const rule of rules) {
    for (const key of Object.keys(rule.when)) {
      if (!seen.includes(key)) seen.push(key);
    }
  }
  return seen;
}

function collectOutputColumns(rules: Rule[]): string[] {
  const seen: string[] = [];
  for (const rule of rules) {
    for (const key of Object.keys(rule.outputs)) {
      if (!seen.includes(key)) seen.push(key);
    }
  }
  return seen;
}

function renderOutputValue(value: unknown): string {
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
}

function parseOutputValue(raw: string): unknown {
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
