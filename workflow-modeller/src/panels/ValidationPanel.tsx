import type { Diagnostic } from '@/domain/types';
import { useValidationSummary } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { useReactFlow } from '@xyflow/react';
import type { ReactNode } from 'react';

const VISIBLE_LIMIT = 50;

function diagnosticKey(d: Diagnostic, i: number): string {
  return [d.code, d.nodeId ?? 'root', d.field ?? '', d.branchKey ?? '', d.boundaryIndex ?? '', i].join('|');
}

export function ValidationPanel(): ReactNode {
  const { errors, warnings, diagnostics, lastRunAt } = useValidationSummary();
  const select = useWorkflowStore((s) => s.select);
  const { setCenter, getNode } = useReactFlow();

  const errorEntries = diagnostics.filter((d) => d.severity === 'error');
  const warningEntries = diagnostics.filter((d) => d.severity === 'warning');

  function focus(d: Diagnostic): void {
    if (!d.nodeId) return;
    const node = getNode(d.nodeId);
    if (node) {
      const cx = node.position.x + (node.measured?.width ?? 180) / 2;
      const cy = node.position.y + (node.measured?.height ?? 80) / 2;
      setCenter(cx, cy, { zoom: 1.1, duration: 250 });
    }
    select({ kind: 'step', id: d.nodeId });
  }

  function renderGroup(title: string, items: Diagnostic[], severityClass: string): ReactNode {
    if (items.length === 0) return null;
    return (
      <section className="wm-validation-group" aria-label={title}>
        <h3 className="wm-validation-group-title">{title}</h3>
        <ul className="wm-diagnostic-list">
          {items.slice(0, VISIBLE_LIMIT).map((d, i) => (
            <li key={diagnosticKey(d, i)} className={`wm-diagnostic wm-diagnostic--${severityClass}`}>
              <button
                type="button"
                className="wm-diagnostic-row"
                onClick={() => focus(d)}
                disabled={!d.nodeId}
                title={d.nodeId ? `Focus ${d.nodeId}` : 'Whole-workflow diagnostic'}
              >
                <code>{d.code}</code>
                {d.nodeId && <span className="wm-diagnostic-node">[{d.nodeId}]</span>}
                <span className="wm-diagnostic-message">{d.message}</span>
              </button>
            </li>
          ))}
          {items.length > VISIBLE_LIMIT && (
            <li className="wm-diagnostic-more">… and {items.length - VISIBLE_LIMIT} more</li>
          )}
        </ul>
      </section>
    );
  }

  return (
    <footer className="wm-validation" aria-label="Validation panel">
      <div className="wm-validation-summary">
        <strong>
          {errors} error(s), {warnings} warning(s)
        </strong>
        <span className="wm-validation-timestamp">
          {lastRunAt ? `Checked ${new Date(lastRunAt).toLocaleTimeString()}` : 'Not run yet'}
        </span>
      </div>
      {diagnostics.length === 0 && lastRunAt !== undefined && (
        <p className="wm-panel-empty">Workflow passed every validation rule.</p>
      )}
      {renderGroup('Errors', errorEntries, 'error')}
      {renderGroup('Warnings', warningEntries, 'warning')}
    </footer>
  );
}
