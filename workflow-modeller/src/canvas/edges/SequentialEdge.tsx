import type { EdgeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { EdgeShell } from './EdgeShell';

export function SequentialEdge(props: EdgeProps): ReactNode {
  return <EdgeShell {...props} variantClass="wm-edge-sequential" />;
}
