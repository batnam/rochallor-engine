import type { NodeProps } from '@xyflow/react';
import type { ReactNode } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export function DecisionNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  if (step.type !== 'DECISION') {
    return <NodeShell data={data} accent="DECISION" />;
  }
  const branches = Object.keys(step.conditionalNextSteps);
  const source = branches.length
    ? branches.map((expr) => ({ id: `branch:${expr}`, label: expr }))
    : [{ id: 'out' }];
  return <NodeShell data={data} accent="DECISION" source={source} />;
}
