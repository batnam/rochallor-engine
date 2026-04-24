import type { EdgeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { EdgeShell } from './EdgeShell';

interface BoundaryEdgeData {
  duration?: string;
  interrupting?: boolean;
}

export function BoundaryEdge(props: EdgeProps): ReactNode {
  const data = (props.data as BoundaryEdgeData | undefined) ?? {};
  const label =
    data.duration || data.interrupting !== undefined ? (
      <span>
        {data.duration ? <code>{data.duration}</code> : null}
        {data.interrupting !== undefined ? (
          <> · {data.interrupting ? 'interrupting' : 'non-interrupting'}</>
        ) : null}
      </span>
    ) : undefined;
  return <EdgeShell {...props} variantClass="wm-edge-boundary" dashed label={label} />;
}
