import {
  BaseEdge,
  EdgeLabelRenderer,
  type EdgeProps,
  MarkerType,
  getBezierPath,
} from '@xyflow/react';
import type { ReactNode } from 'react';

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

  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        className={variantClass}
        markerEnd={marker ?? `url(#${MarkerType.ArrowClosed})`}
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
