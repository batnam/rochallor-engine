import {
  Background,
  Controls,
  type Edge,
  type EdgeTypes,
  MiniMap,
  type Node,
  type NodeTypes,
  ReactFlow,
  useReactFlow,
} from '@xyflow/react';
import { type DragEvent, type ReactNode, useCallback, useEffect, useMemo } from 'react';
import '@xyflow/react/dist/style.css';

import type { GraphEdge, GraphNode, Step, StepId, StepType } from '@/domain/types';
import { DRAG_MIME } from '@/panels/Palette';
import { useEdges, useNodes } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { BoundaryEdge } from './edges/BoundaryEdge';
import { ConditionalEdge } from './edges/ConditionalEdge';
import { ParallelEdge } from './edges/ParallelEdge';
import { SequentialEdge } from './edges/SequentialEdge';
import { layoutLeftToRight } from './layout';
import { DecisionNode } from './nodes/DecisionNode';
import { EndNode } from './nodes/EndNode';
import { JoinGatewayNode } from './nodes/JoinGatewayNode';
import type { NodeData } from './nodes/NodeShell';
import { ParallelGatewayNode } from './nodes/ParallelGatewayNode';
import { ServiceTaskNode } from './nodes/ServiceTaskNode';
import { TransformationNode } from './nodes/TransformationNode';
import { UserTaskNode } from './nodes/UserTaskNode';
import { WaitNode } from './nodes/WaitNode';

const nodeTypes: NodeTypes = {
  SERVICE_TASK: ServiceTaskNode,
  USER_TASK: UserTaskNode,
  DECISION: DecisionNode,
  TRANSFORMATION: TransformationNode,
  WAIT: WaitNode,
  PARALLEL_GATEWAY: ParallelGatewayNode,
  JOIN_GATEWAY: JoinGatewayNode,
  END: EndNode,
};

const edgeTypes: EdgeTypes = {
  sequential: SequentialEdge,
  conditional: ConditionalEdge,
  parallel: ParallelEdge,
  'join-out': SequentialEdge,
  'join-target': SequentialEdge,
  boundary: BoundaryEdge,
};

function mapToFlowNodes(
  nodes: GraphNode[],
  layout: Record<StepId, { x: number; y: number }>,
): Node<NodeData>[] {
  return nodes.map((n) => ({
    id: n.id,
    type: n.type,
    position: layout[n.id] ?? { x: 0, y: 0 },
    data: { step: n.step, isEntry: n.isEntry },
  }));
}

function boundaryEventFor(
  step: Step,
  index: number,
): { duration: string; interrupting: boolean } | undefined {
  if (step.type !== 'SERVICE_TASK' && step.type !== 'USER_TASK' && step.type !== 'WAIT') {
    return undefined;
  }
  return step.boundaryEvents?.[index];
}

function mapToFlowEdges(edges: GraphEdge[], nodes: GraphNode[]): Edge[] {
  const byId = new Map(nodes.map((n) => [n.id, n.step]));
  return edges.map((e) => {
    const base: Edge = {
      id: e.id,
      source: e.from,
      target: e.to,
      type: e.variant.kind,
      data: {},
    };
    if (e.variant.kind === 'conditional') {
      return { ...base, data: { expression: e.variant.expression } };
    }
    if (e.variant.kind === 'boundary') {
      const source = byId.get(e.from);
      const evt = source ? boundaryEventFor(source, e.variant.index) : undefined;
      return {
        ...base,
        data: evt
          ? { duration: evt.duration, interrupting: evt.interrupting }
          : { interrupting: false },
      };
    }
    return base;
  });
}

const STEP_TYPE_SET = new Set<StepType>([
  'SERVICE_TASK',
  'USER_TASK',
  'DECISION',
  'TRANSFORMATION',
  'WAIT',
  'PARALLEL_GATEWAY',
  'JOIN_GATEWAY',
  'END',
]);

function CanvasInner(): ReactNode {
  const nodes = useNodes();
  const edges = useEdges();
  const storedLayout = useWorkflowStore((s) => s.layout);
  const addStep = useWorkflowStore((s) => s.addStep);
  const setLayout = useWorkflowStore((s) => s.setLayout);
  const { fitView, screenToFlowPosition } = useReactFlow();

  const onDragOver = useCallback((e: DragEvent<HTMLDivElement>) => {
    if (e.dataTransfer.types.includes(DRAG_MIME)) {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
    }
  }, []);

  const onDrop = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      const raw = e.dataTransfer.getData(DRAG_MIME);
      if (!raw) return;
      if (!STEP_TYPE_SET.has(raw as StepType)) return;
      e.preventDefault();
      const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
      const id = addStep({ type: raw as StepType });
      setLayout(id, position);
    },
    [addStep, setLayout, screenToFlowPosition],
  );

  const onConnect = useCallback((connection: { source: string | null; target: string | null }) => {
    const { source, target } = connection;
    if (!source || !target || source === target) return;
    const state = useWorkflowStore.getState();
    const src = state.definition.steps.find((s) => s.id === source);
    if (!src) return;
    switch (src.type) {
      case 'SERVICE_TASK':
      case 'USER_TASK':
      case 'WAIT':
      case 'TRANSFORMATION':
      case 'JOIN_GATEWAY':
        state.updateStepProperty(source, 'nextStep', target);
        break;
      case 'DECISION': {
        const expr = window.prompt('Branch expression (boolean):', 'value == "X"');
        if (!expr) return;
        state.updateStepProperty(source, 'conditionalNextSteps', {
          ...src.conditionalNextSteps,
          [expr]: target,
        });
        break;
      }
      case 'PARALLEL_GATEWAY': {
        if (src.parallelNextSteps.includes(target)) return;
        state.updateStepProperty(source, 'parallelNextSteps', [...src.parallelNextSteps, target]);
        break;
      }
      case 'END':
        return;
    }
  }, []);

  const layout = useMemo(() => {
    if (nodes.length === 0) return {};
    const missing = nodes.some((n) => !(n.id in storedLayout));
    return missing ? { ...layoutLeftToRight(nodes, edges), ...storedLayout } : storedLayout;
  }, [nodes, edges, storedLayout]);

  const flowNodes = useMemo(() => mapToFlowNodes(nodes, layout), [nodes, layout]);
  const flowEdges = useMemo(() => mapToFlowEdges(edges, nodes), [edges, nodes]);

  useEffect(() => {
    if (flowNodes.length === 0) return;
    const timer = setTimeout(() => fitView({ duration: 250, padding: 0.2 }), 50);
    return () => clearTimeout(timer);
  }, [flowNodes, fitView]);

  return (
    <div className="wm-canvas-inner" onDragOver={onDragOver} onDrop={onDrop}>
      {nodes.length === 0 && (
        <div className="wm-canvas-empty">
          <h2>Empty canvas</h2>
          <p>Drop a step from the palette, click a tile, or import JSON.</p>
        </div>
      )}
      <ReactFlow
        nodes={flowNodes}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        fitView={nodes.length > 0}
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_, n) => useWorkflowStore.getState().select({ kind: 'step', id: n.id })}
        onPaneClick={() => useWorkflowStore.getState().select({ kind: 'none' })}
        onNodeDragStop={(_, n) => useWorkflowStore.getState().setLayout(n.id, n.position)}
        onConnect={onConnect}
      >
        <Background gap={16} />
        {nodes.length > 0 && <MiniMap pannable zoomable />}
        {nodes.length > 0 && <Controls showInteractive={false} />}
      </ReactFlow>
    </div>
  );
}

export function Canvas(): ReactNode {
  return <CanvasInner />;
}

export { ReactFlowProvider } from '@xyflow/react';
