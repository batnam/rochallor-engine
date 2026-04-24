import type { Step, StepType } from '@/domain/types';
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

  return (
    <div
      className={`wm-node wm-node--${accent.toLowerCase()} ${data.isEntry ? 'wm-node--entry' : ''}`}
    >
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
