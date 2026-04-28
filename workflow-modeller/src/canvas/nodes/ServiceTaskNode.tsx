import type { NodeProps } from '@xyflow/react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const ServiceTaskNode = memo(function ServiceTaskNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  const subtitle =
    step.type === 'SERVICE_TASK' && step.jobType ? `job: ${step.jobType}` : undefined;
  return <NodeShell data={data} accent="SERVICE_TASK" subtitle={subtitle} />;
});
