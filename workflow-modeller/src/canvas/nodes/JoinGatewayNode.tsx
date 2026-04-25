import type { NodeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export function JoinGatewayNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  return <NodeShell data={data} accent="JOIN_GATEWAY" />;
}
