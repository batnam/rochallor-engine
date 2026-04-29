import type { NodeProps } from '@xyflow/react';
import { Clock } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const WaitNode = memo(function WaitNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  return <NodeShell data={data} accent="WAIT" shape="circle" icon={<Clock size={12} />} />;
});
