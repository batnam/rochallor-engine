import type { NodeProps } from '@xyflow/react';
import { GitFork } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const ParallelGatewayNode = memo(function ParallelGatewayNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  if (step.type !== 'PARALLEL_GATEWAY') {
    return (
      <NodeShell
        data={data}
        accent="PARALLEL_GATEWAY"
        shape="diamond-sm"
        icon={<GitFork size={28} />}
      />
    );
  }
  const source = step.parallelNextSteps.length
    ? step.parallelNextSteps.map((_, i) => ({ id: `parallel:${i}` }))
    : [{ id: 'out' }];
  return (
    <NodeShell
      data={data}
      accent="PARALLEL_GATEWAY"
      shape="diamond-sm"
      icon={<GitFork size={28} />}
      source={source}
    />
  );
});
