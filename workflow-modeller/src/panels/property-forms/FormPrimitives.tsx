import type { Step, StepType } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';

interface FieldProps {
  label: string;
  children: ReactNode;
  hint?: string;
}

export function Field({ label, children, hint }: FieldProps): ReactNode {
  return (
    <div className="wm-field">
      <span className="wm-field-label">{label}</span>
      {children}
      {hint && <span className="wm-field-hint">{hint}</span>}
    </div>
  );
}

interface TextInputProps {
  value: string;
  onCommit: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
}

export function TextInput({ value, onCommit, placeholder, disabled }: TextInputProps): ReactNode {
  return (
    <input
      type="text"
      className="wm-input"
      defaultValue={value}
      key={value}
      placeholder={placeholder}
      disabled={disabled}
      onBlur={(e) => {
        if (e.target.value !== value) onCommit(e.target.value);
      }}
    />
  );
}

interface NumberInputProps {
  value: number | undefined;
  onCommit: (value: number | undefined) => void;
  min?: number;
  placeholder?: string;
}

export function NumberInput({ value, onCommit, min, placeholder }: NumberInputProps): ReactNode {
  return (
    <input
      type="number"
      className="wm-input"
      defaultValue={value ?? ''}
      key={value ?? ''}
      min={min}
      placeholder={placeholder}
      onBlur={(e) => {
        const v = e.target.value.trim();
        if (v === '') {
          onCommit(undefined);
          return;
        }
        const n = Number(v);
        if (!Number.isNaN(n)) onCommit(n);
      }}
    />
  );
}

interface StepPickerProps {
  value: string;
  onCommit: (value: string) => void;
  filter?: (step: Step) => boolean;
  excludeIds?: string[];
  allowEmpty?: boolean;
}

export function StepPicker({
  value,
  onCommit,
  filter,
  excludeIds,
  allowEmpty,
}: StepPickerProps): ReactNode {
  const steps = useWorkflowStore((s) => s.definition.steps);
  const candidates = steps.filter((s) => (!filter || filter(s)) && !excludeIds?.includes(s.id));
  return (
    <select className="wm-input" value={value} onChange={(e) => onCommit(e.target.value)}>
      {allowEmpty && <option value="">(none)</option>}
      {candidates.map((s) => (
        <option key={s.id} value={s.id}>
          {s.id} — {labelFor(s.type)}
        </option>
      ))}
      {value && !candidates.some((c) => c.id === value) && (
        <option value={value}>⚠ {value} (missing)</option>
      )}
    </select>
  );
}

function labelFor(type: StepType): string {
  return type.replace(/_/g, ' ').toLowerCase();
}

interface KeyValueListProps {
  entries: Array<{ key: string; value: string }>;
  onChange: (entries: Array<{ key: string; value: string }>) => void;
  keyLabel?: string;
  valueLabel?: string;
  valueRender?: (entry: { key: string; value: string }, setValue: (v: string) => void) => ReactNode;
}

export function KeyValueList({
  entries,
  onChange,
  keyLabel = 'key',
  valueLabel = 'value',
  valueRender,
}: KeyValueListProps): ReactNode {
  function patch(i: number, partial: Partial<{ key: string; value: string }>): void {
    const next = entries.map((e, idx) => (idx === i ? { ...e, ...partial } : e));
    onChange(next);
  }
  function remove(i: number): void {
    onChange(entries.filter((_, idx) => idx !== i));
  }
  function move(i: number, dir: -1 | 1): void {
    const next = [...entries];
    const target = i + dir;
    if (target < 0 || target >= next.length) return;
    const a = next[i];
    const b = next[target];
    if (!a || !b) return;
    next[target] = a;
    next[i] = b;
    onChange(next);
  }
  function add(): void {
    onChange([...entries, { key: '', value: '' }]);
  }

  return (
    <div className="wm-keyvalue">
      <div className="wm-keyvalue-header">
        <span>{keyLabel}</span>
        <span>{valueLabel}</span>
      </div>
      {entries.map((entry, i) => (
        <div key={`${i}-${entry.key}`} className="wm-keyvalue-row">
          <input
            type="text"
            className="wm-input"
            defaultValue={entry.key}
            onBlur={(e) => patch(i, { key: e.target.value })}
          />
          {valueRender ? (
            valueRender(entry, (v) => patch(i, { value: v }))
          ) : (
            <input
              type="text"
              className="wm-input"
              defaultValue={entry.value}
              onBlur={(e) => patch(i, { value: e.target.value })}
            />
          )}
          <button type="button" onClick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">
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
          <button type="button" onClick={() => remove(i)} aria-label="Remove row">
            ×
          </button>
        </div>
      ))}
      <button type="button" className="wm-keyvalue-add" onClick={add}>
        + add row
      </button>
    </div>
  );
}
