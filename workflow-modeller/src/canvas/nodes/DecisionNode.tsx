import type { NodeProps } from '@xyflow/react';
import { HelpCircle } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const DecisionNode = memo(function DecisionNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  if (step.type !== 'DECISION') {
    return (
      <NodeShell data={data} accent="DECISION" shape="diamond" icon={<HelpCircle size={24} />} />
    );
  }
  const branches = Object.keys(step.conditionalNextSteps);
  const source = branches.length
    ? branches.map((expr) => ({ id: `branch:${expr}`, label: expr }))
    : [{ id: 'out' }];
  return (
    <NodeShell
      data={data}
      accent="DECISION"
      shape="diamond"
      icon={<HelpCircle size={24} />}
      source={source}
    />
  );
});
