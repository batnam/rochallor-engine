import type { EdgeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { EdgeShell } from './EdgeShell';

export function ConditionalEdge(props: EdgeProps): ReactNode {
  const expression = (props.data as { expression?: string } | undefined)?.expression;
  return (
    <EdgeShell
      {...props}
      variantClass="wm-edge-conditional"
      label={expression ? <code>{expression}</code> : undefined}
    />
  );
}
