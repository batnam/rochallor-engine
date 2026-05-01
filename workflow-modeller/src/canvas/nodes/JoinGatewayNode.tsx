import type { NodeProps } from '@xyflow/react';
import { GitMerge } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const JoinGatewayNode = memo(function JoinGatewayNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  return (
    <NodeShell data={data} accent="JOIN_GATEWAY" shape="diamond-sm" icon={<GitMerge size={28} />} />
  );
});
