import type { NodeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export function UserTaskNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  const subtitle = step.type === 'USER_TASK' && step.jobType ? `job: ${step.jobType}` : undefined;
  return <NodeShell data={data} accent="USER_TASK" subtitle={subtitle} />;
}
