import type { NodeProps } from '@xyflow/react';
import { Table } from 'lucide-react';
import { type ReactNode, memo } from 'react';
import { type NodeData, NodeShell } from './NodeShell';

export const DecisionTableNode = memo(function DecisionTableNode(props: NodeProps): ReactNode {
  const data = props.data as NodeData;
  const step = data.step;
  if (step.type !== 'DECISION_TABLE') {
    return <NodeShell data={data} accent="DECISION_TABLE" icon={<Table size={24} />} />;
  }
  const ruleCount = step.decisionTable.rules.length;
  const hitPolicy = step.hitPolicy ?? 'U';
  const subtitle =
    ruleCount === 0 ? hitPolicy : `${ruleCount} rule${ruleCount === 1 ? '' : 's'} · ${hitPolicy}`;
  return (
    <NodeShell
      data={data}
      accent="DECISION_TABLE"
      icon={<Table size={24} />}
      source={[{ id: 'out' }]}
      subtitle={subtitle}
    />
  );
});
