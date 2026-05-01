import { BaseEdge, EdgeLabelRenderer, type EdgeProps, getSmoothStepPath } from '@xyflow/react';
import type { ReactNode } from 'react';

const VARIANT_ARROW_COLOR: Record<string, string> = {
  'wm-edge-sequential': '#334155',
  'wm-edge-conditional': '#f97316',
  'wm-edge-parallel': '#14b8a6',
  'wm-edge-boundary': '#dc2626',
};

export interface EdgeShellProps extends EdgeProps {
  variantClass: string;
  label?: ReactNode;
  dashed?: boolean;
  marker?: string;
}

export function EdgeShell(props: EdgeShellProps): ReactNode {
  const {
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    id,
    variantClass,
    label,
    dashed,
    marker,
    style,
  } = props;

  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 10,
  });

  const arrowColor = VARIANT_ARROW_COLOR[variantClass] ?? '#334155';
  const markerId = `wm-arrow-${variantClass}`;

  return (
    <>
      <defs>
        <marker
          id={markerId}
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerUnits="strokeWidth"
          markerWidth="5"
          markerHeight="5"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" fill={arrowColor} />
        </marker>
      </defs>
      <BaseEdge
        id={id}
        path={path}
        className={variantClass}
        markerEnd={marker ?? `url(#${markerId})`}
        style={{ ...style, ...(dashed ? { strokeDasharray: '6 4' } : {}) }}
      />
      {label && (
        <EdgeLabelRenderer>
          <div
            className={`wm-edge-label ${variantClass}-label`}
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
