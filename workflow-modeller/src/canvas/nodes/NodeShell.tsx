import type { Step, StepType } from '@/domain/types';
import { useDiagnosticsForNode } from '@/store/selectors';
import { Handle, Position } from '@xyflow/react';
import type { ReactNode } from 'react';

export interface NodeData extends Record<string, unknown> {
  step: Step;
  isEntry: boolean;
}

interface NodeShellProps {
  data: NodeData;
  accent: StepType;
  showTarget?: boolean;
  source?: { id: string; label?: string; offsetPct?: number }[];
  extra?: ReactNode;
  subtitle?: ReactNode;
}

export function NodeShell(props: NodeShellProps): ReactNode {
  const { data, accent, showTarget = true, source = [{ id: 'out' }], extra, subtitle } = props;
  const { errors, warnings, diagnostics } = useDiagnosticsForNode(data.step.id);

  const classes = [
    'wm-node',
    `wm-node--${accent.toLowerCase()}`,
    data.isEntry && 'wm-node--entry',
    errors > 0 && 'wm-node--has-error',
    errors === 0 && warnings > 0 && 'wm-node--has-warning',
  ]
    .filter(Boolean)
    .join(' ');

  const badgeMessages = diagnostics.map((d) => `${d.code}: ${d.message}`).join('\n');

  return (
    <div className={classes}>
      {showTarget && <Handle type="target" position={Position.Left} id="in" />}

      <div className="wm-node-header">
        <span className="wm-node-badge">{accent.replace('_', ' ')}</span>
        {data.isEntry && <span className="wm-node-entry-tag">ENTRY</span>}
      </div>
      <div className="wm-node-title" title={data.step.id}>
        {data.step.name}
      </div>
      <div className="wm-node-id">{data.step.id}</div>
      {subtitle && <div className="wm-node-subtitle">{subtitle}</div>}
      {extra}

      {(errors > 0 || warnings > 0) && (
        <div
          className={`wm-node-diagnostic wm-node-diagnostic--${errors > 0 ? 'error' : 'warning'}`}
          title={badgeMessages}
          aria-label={`${errors} error(s), ${warnings} warning(s) on this step`}
        >
          {errors > 0 ? `⛔ ${errors}` : `⚠ ${warnings}`}
        </div>
      )}

      {source.map((s, i) => (
        <Handle
          key={s.id}
          type="source"
          position={Position.Right}
          id={s.id}
          style={
            source.length > 1
              ? { top: `${s.offsetPct ?? ((i + 1) * 100) / (source.length + 1)}%` }
              : undefined
          }
        >
          {source.length > 1 && s.label ? <span className="wm-handle-label">{s.label}</span> : null}
        </Handle>
      ))}
    </div>
  );
}
