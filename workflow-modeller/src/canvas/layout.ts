import type { GraphEdge, GraphNode, StepId } from '@/domain/types';
import dagre from 'dagre';

const NODE_WIDTH = 180;
const NODE_HEIGHT = 56;

export function layoutLeftToRight(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Record<StepId, { x: number; y: number }> {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'LR', nodesep: 40, ranksep: 80 });
  g.setDefaultEdgeLabel(() => ({}));

  for (const node of nodes) {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const edge of edges) {
    g.setEdge(edge.from, edge.to);
  }

  dagre.layout(g);

  const positions: Record<StepId, { x: number; y: number }> = {};
  for (const node of nodes) {
    const laidOut = g.node(node.id);
    if (!laidOut) continue;
    positions[node.id] = {
      x: laidOut.x - NODE_WIDTH / 2,
      y: laidOut.y - NODE_HEIGHT / 2,
    };
  }
  return positions;
}

/** Merge auto-layout positions with any existing manual positions (manual wins). */
export function tidyLayout(
  nodes: GraphNode[],
  edges: GraphEdge[],
  existing: Record<StepId, { x: number; y: number }> = {},
): Record<StepId, { x: number; y: number }> {
  const auto = layoutLeftToRight(nodes, edges);
  return { ...auto, ...existing };
}
