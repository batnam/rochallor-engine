import type { Step, StepType } from '@/domain/types';
import { useDiagnosticsForNode } from '@/store/selectors';
import { Handle, Position } from '@xyflow/react';
import type { CSSProperties, ReactNode } from 'react';

const STEP_ACCENT: Record<StepType, string> = {
  SERVICE_TASK: '#3b82f6',
  USER_TASK: '#22c55e',
  DECISION: '#ef4444',
  TRANSFORMATION: '#a855f7',
  WAIT: '#64748b',
  PARALLEL_GATEWAY: '#06b6d4',
  JOIN_GATEWAY: '#f59e0b',
  END: '#0f172a',
};

export interface NodeData extends Record<string, unknown> {
  step: Step;
  isEntry: boolean;
}

interface NodeShellProps {
  data: NodeData;
  accent: StepType;
  icon?: ReactNode;
  shape?: 'default' | 'circle' | 'diamond' | 'diamond-sm';
  active?: boolean;
  showTarget?: boolean;
  source?: { id: string; label?: string; offsetPct?: number }[];
  extra?: ReactNode;
  subtitle?: ReactNode;
}

export function NodeShell(props: NodeShellProps): ReactNode {
  const {
    data,
    accent,
    icon,
    shape = 'default',
    active = false,
    showTarget = true,
    source = [{ id: 'out' }],
    extra,
    subtitle,
  } = props;
  const { errors, warnings, diagnostics } = useDiagnosticsForNode(data.step.id);

  const isDiamond = shape === 'diamond' || shape === 'diamond-sm';

  const classes = [
    'wm-node',
    `wm-node--${accent.toLowerCase()}`,
    shape !== 'default' && `wm-node--${shape}`,
    data.isEntry && 'wm-node--entry',
    active && 'wm-node--active',
    errors > 0 && 'wm-node--has-error',
    errors === 0 && warnings > 0 && 'wm-node--has-warning',
  ]
    .filter(Boolean)
    .join(' ');

  const badgeMessages = diagnostics.map((d) => `${d.code}: ${d.message}`).join('\n');

  const accentStyle = active
    ? ({ '--accent-color': STEP_ACCENT[accent] } as CSSProperties)
    : undefined;

  const isSingleOut = source.length === 1 && source[0]?.id === 'out';

  const handles = (
    <>
      {showTarget && (
        <>
          <Handle
            type="target"
            position={isDiamond ? Position.Top : Position.Left}
            id="in"
            style={isDiamond ? { left: 0, top: 0, transform: 'translate(-50%, -50%)' } : undefined}
          />
          {!isDiamond && (
            <Handle type="target" position={Position.Top} id="in-top" className="wm-handle-side" />
          )}
          <Handle
            type="target"
            position={Position.Right}
            id="in-right"
            className="wm-handle-side"
          />
          <Handle
            type="target"
            position={Position.Bottom}
            id="in-bottom"
            className="wm-handle-side"
          />
          {isDiamond && (
            <>
              {/* Upper-left edge of diamond — the previously missing target anchor. */}
              <Handle
                type="target"
                position={Position.Left}
                id="in-edge-tl"
                className="wm-handle-side"
              />
              {/* RIGHT, BOTTOM, LEFT corners of diamond (TOP corner is `in`). */}
              <Handle
                type="target"
                position={Position.Right}
                id="in-c-right"
                style={{ top: 0 }}
                className="wm-handle-side"
              />
              <Handle
                type="target"
                position={Position.Bottom}
                id="in-c-bottom"
                style={{ top: '100%', left: '100%', transform: 'translate(-50%, -50%)' }}
                className="wm-handle-side"
              />
              <Handle
                type="target"
                position={Position.Left}
                id="in-c-left"
                style={{ top: '100%' }}
                className="wm-handle-side"
              />
            </>
          )}
        </>
      )}
      {source.map((s, i) => (
        <Handle
          key={s.id}
          type="source"
          position={isDiamond ? Position.Bottom : Position.Right}
          id={s.id}
          style={
            isDiamond
              ? { top: '100%', left: '100%', transform: 'translate(-50%, -50%)' }
              : source.length > 1
                ? { top: `${s.offsetPct ?? ((i + 1) * 100) / (source.length + 1)}%` }
                : undefined
          }
        >
          {source.length > 1 && s.label ? <span className="wm-handle-label">{s.label}</span> : null}
        </Handle>
      ))}
      {isSingleOut && !isDiamond && (
        <>
          <Handle type="source" position={Position.Top} id="out-top" className="wm-handle-side" />
          <Handle
            type="source"
            position={Position.Bottom}
            id="out-bottom"
            className="wm-handle-side"
          />
          <Handle type="source" position={Position.Left} id="out-left" className="wm-handle-side" />
        </>
      )}
      {isDiamond && (
        // Reroute-only ghost source handles at every diamond anchor except the
        // BOTTOM corner (where the main source / branches already live). These
        // let the user drag an existing edge endpoint onto any side or corner
        // of the diamond — the override saved by Canvas keeps the new routing.
        <>
          <Handle
            type="source"
            position={Position.Top}
            id="out-c-top"
            style={{ left: 0 }}
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Right}
            id="out-c-right"
            style={{ top: 0 }}
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Left}
            id="out-c-left"
            style={{ top: '100%' }}
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Left}
            id="out-edge-tl"
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Top}
            id="out-edge-tr"
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Right}
            id="out-edge-br"
            className="wm-handle-side"
          />
          <Handle
            type="source"
            position={Position.Bottom}
            id="out-edge-bl"
            style={{ top: '100%', left: '100%', transform: 'translate(-50%, -50%)' }}
            className="wm-handle-side"
          />
        </>
      )}
    </>
  );

  const innerContent = (
    <>
      <div className="wm-node-header">
        {icon && <span className="wm-node-icon">{icon}</span>}
        <span className="wm-node-badge">{accent.replace(/_/g, ' ')}</span>
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
    </>
  );

  // All diamonds: name floats above, only type-specific content inside.
  // Decision: "DECISION" type label. Gateways: icon only.
  const diamondContent =
    shape === 'diamond' ? (
      <div className="wm-node-diamond-type">{accent.replace(/_/g, ' ')}</div>
    ) : (
      <span className="wm-node-diamond-icon">{icon}</span>
    );

  return (
    <div className={classes} style={accentStyle}>
      {isDiamond && (
        <div className="wm-diamond-float-name" title={data.step.name}>
          {data.step.name}
        </div>
      )}
      {handles}
      {isDiamond ? <div className="wm-node-inner">{diamondContent}</div> : innerContent}
    </div>
  );
}
