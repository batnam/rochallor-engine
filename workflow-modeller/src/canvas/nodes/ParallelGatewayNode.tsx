import type { NodeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export function ParallelGatewayNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  if (step.type !== 'PARALLEL_GATEWAY') {
    return <NodeShell data={data} accent="PARALLEL_GATEWAY" />;
  }
  const source = step.parallelNextSteps.length
    ? step.parallelNextSteps.map((target, i) => ({ id: `parallel:${i}`, label: target }))
    : [{ id: 'out' }];
  return <NodeShell data={data} accent="PARALLEL_GATEWAY" source={source} />;
}
