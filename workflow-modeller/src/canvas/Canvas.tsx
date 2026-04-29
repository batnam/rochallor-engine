import {
  Background,
  Controls,
  type Edge,
  type EdgeTypes,
  MiniMap,
  type Node,
  type NodeTypes,
  ReactFlow,
  useEdgesState,
  useNodesState,
  useReactFlow,
} from '@xyflow/react';
import { type DragEvent, type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import '@xyflow/react/dist/style.css';

import type { GraphEdge, GraphNode, Step, StepId, StepType } from '@/domain/types';
import { DRAG_MIME } from '@/panels/Palette';
import { useEdges, useNodes } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { BoundaryEdge } from './edges/BoundaryEdge';
import { ConditionalEdge } from './edges/ConditionalEdge';
import { ParallelEdge } from './edges/ParallelEdge';
import { SequentialEdge } from './edges/SequentialEdge';
import { layoutWithElk } from './layout';
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
      sourceHandle: e.sourceHandle ?? null,
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
  const source = useWorkflowStore((s) => s.source);
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

  const [elkLayout, setElkLayout] = useState<Record<string, { x: number; y: number }>>({});

  useEffect(() => {
    if (nodes.length === 0) {
      setElkLayout({});
      return;
    }
    const missing = nodes.some((n) => !(n.id in storedLayout) && !(n.id in elkLayout));
    if (!missing) return;
    layoutWithElk(nodes, edges).then(setElkLayout);
  }, [nodes, edges, storedLayout, elkLayout]);

  const layout = useMemo(
    () => ({ ...elkLayout, ...storedLayout }),
    [elkLayout, storedLayout],
  );

  // ReactFlow's internal node/edge state. We sync from the store one-way so
  // drag interactions can update positions locally without thrashing the store
  // — the final position is committed via onNodeDragStop. Without this pattern
  // ReactFlow has no channel to express the in-flight drag position, leading
  // to jerky frame drops while dragging.
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<Node<NodeData>>([]);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState<Edge>([]);

  useEffect(() => {
    setRfNodes(mapToFlowNodes(nodes, layout));
  }, [nodes, layout, setRfNodes]);

  useEffect(() => {
    setRfEdges(mapToFlowEdges(edges, nodes));
  }, [edges, nodes, setRfEdges]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `source` is intentionally a trigger-only dep — the effect body reads from getState() so it doesn't reference `source` directly, but ref changes (import / load / upload / reset) must re-fit; step edits must NOT.
  useEffect(() => {
    const timer = setTimeout(() => {
      if (useWorkflowStore.getState().definition.steps.length > 0) {
        fitView({ duration: 250, padding: 0.2 });
      }
    }, 50);
    return () => clearTimeout(timer);
  }, [source, fitView]);

  return (
    <div className="wm-canvas-inner" onDragOver={onDragOver} onDrop={onDrop}>
      {nodes.length === 0 && (
        <div className="wm-canvas-empty">
          <h2>Empty canvas</h2>
          <p>Drop a step from the palette, click a tile, or import JSON.</p>
        </div>
      )}
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
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
