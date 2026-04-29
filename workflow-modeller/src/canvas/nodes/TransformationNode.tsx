import type { NodeProps } from '@xyflow/react';
import { Wand2 } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const TransformationNode = memo(function TransformationNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  const subtitle =
    step.type === 'TRANSFORMATION'
      ? `${Object.keys(step.transformations).length} transformation(s)`
      : undefined;
  return (
    <NodeShell data={data} accent="TRANSFORMATION" icon={<Wand2 size={24} />} subtitle={subtitle} />
  );
});
