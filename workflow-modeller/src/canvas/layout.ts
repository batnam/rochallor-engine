import type { ElkExtendedEdge, ElkNode, ElkPort } from 'elkjs';
import ELK from 'elkjs/lib/elk.bundled.js';
import type { GraphEdge, GraphNode, StepId } from '@/domain/types';

const elk = new ELK();

const NODE_WIDTH = 180;

const ELK_OPTIONS: Record<string, string> = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.spacing.nodeNode': '80',
  'elk.layered.spacing.nodeNodeBetweenLayers': '140',
  'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  'elk.padding': '[top=30, left=30, bottom=30, right=30]',
};

function nodeHeight(node: GraphNode): number {
  if (node.step.type === 'DECISION') {
    return Math.max(70, Object.keys(node.step.conditionalNextSteps).length * 28);
  }
  if (node.step.type === 'PARALLEL_GATEWAY') {
    return Math.max(70, node.step.parallelNextSteps.length * 28);
  }
  return 70;
}

function nodePorts(node: GraphNode): ElkPort[] {
  const ports: ElkPort[] = [
    { id: `${node.id}__in`, layoutOptions: { 'port.side': 'WEST' } },
  ];
  if (node.step.type === 'DECISION') {
    Object.keys(node.step.conditionalNextSteps).forEach((_, i) => {
      ports.push({ id: `${node.id}__branch_${i}`, layoutOptions: { 'port.side': 'EAST' } });
    });
  } else if (node.step.type === 'PARALLEL_GATEWAY') {
    node.step.parallelNextSteps.forEach((_, i) => {
      ports.push({ id: `${node.id}__parallel_${i}`, layoutOptions: { 'port.side': 'EAST' } });
    });
  } else if (node.step.type !== 'END') {
    ports.push({ id: `${node.id}__out`, layoutOptions: { 'port.side': 'EAST' } });
  }
  return ports;
}

function elkSourcePort(edge: GraphEdge, source: GraphNode): string {
  if (edge.variant.kind === 'conditional' && source.step.type === 'DECISION') {
    const idx = Object.keys(source.step.conditionalNextSteps).indexOf(edge.variant.expression);
    return `${edge.from}__branch_${idx >= 0 ? idx : 0}`;
  }
  if (edge.variant.kind === 'parallel') {
    const i = edge.sourceHandle ? edge.sourceHandle.replace('parallel:', '') : '0';
    return `${edge.from}__parallel_${i}`;
  }
  return `${edge.from}__out`;
}

export async function layoutWithElk(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Promise<Record<StepId, { x: number; y: number }>> {
  if (nodes.length === 0) return {};

  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  const graph: ElkNode = {
    id: 'root',
    layoutOptions: ELK_OPTIONS,
    children: nodes.map((n) => ({
      id: n.id,
      width: NODE_WIDTH,
      height: nodeHeight(n),
      ports: nodePorts(n),
      layoutOptions: { 'elk.portConstraints': 'FIXED_SIDE' },
    })),
    edges: edges
      .filter((e) => nodeById.has(e.from) && nodeById.has(e.to))
      .map(
        (e): ElkExtendedEdge => ({
          id: e.id,
          sources: [elkSourcePort(e, nodeById.get(e.from)!)],
          targets: [`${e.to}__in`],
        }),
      ),
  };

  const laid = await elk.layout(graph);

  const positions: Record<StepId, { x: number; y: number }> = {};
  for (const child of laid.children ?? []) {
    if (child.x != null && child.y != null) {
      positions[child.id] = { x: child.x, y: child.y };
    }
  }
  return positions;
}
