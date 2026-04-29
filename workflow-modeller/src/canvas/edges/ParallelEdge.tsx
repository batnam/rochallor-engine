import type { EdgeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { EdgeShell } from './EdgeShell';

export function ParallelEdge(props: EdgeProps): ReactNode {
  return <EdgeShell {...props} variantClass="wm-edge-parallel" style={{ strokeWidth: 3 }} />;
}
