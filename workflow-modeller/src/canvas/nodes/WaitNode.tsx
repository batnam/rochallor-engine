import type { NodeProps } from '@xyflow/react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const WaitNode = memo(function WaitNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  return <NodeShell data={data} accent="WAIT" />;
});
