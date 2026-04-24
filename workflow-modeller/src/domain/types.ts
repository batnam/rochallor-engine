import type { z } from 'zod';
import type {
  zBoundaryEvent,
  zDecision,
  zEnd,
  zJoinGateway,
  zParallelGateway,
  zServiceTask,
  zStep,
  zTransformation,
  zUserTask,
  zWait,
  zWorkflowDefinition,
} from './schema';

export type StepType =
  | 'SERVICE_TASK'
  | 'USER_TASK'
  | 'DECISION'
  | 'TRANSFORMATION'
  | 'WAIT'
  | 'PARALLEL_GATEWAY'
  | 'JOIN_GATEWAY'
  | 'END';

export type StepId = string;

export type ServiceTaskStep = z.infer<typeof zServiceTask>;
export type UserTaskStep = z.infer<typeof zUserTask>;
export type DecisionStep = z.infer<typeof zDecision>;
export type TransformationStep = z.infer<typeof zTransformation>;
export type WaitStep = z.infer<typeof zWait>;
export type ParallelGatewayStep = z.infer<typeof zParallelGateway>;
export type JoinGatewayStep = z.infer<typeof zJoinGateway>;
export type EndStep = z.infer<typeof zEnd>;

export type Step = z.infer<typeof zStep>;
export type WorkflowDefinition = z.infer<typeof zWorkflowDefinition>;
export type BoundaryEvent = z.infer<typeof zBoundaryEvent>;

export type EdgeVariant =
  | { kind: 'sequential' }
  | { kind: 'conditional'; expression: string }
  | { kind: 'parallel' }
  | { kind: 'join-target' }
  | { kind: 'join-out' }
  | { kind: 'boundary'; index: number };

export type DiagnosticCode =
  | 'ID_FORMAT'
  | 'NAME_REQUIRED'
  | 'STEPS_NONEMPTY'
  | 'STEP_ID_UNIQUE'
  | 'STEP_ID_REQUIRED'
  | 'STEP_TYPE_VALID'
  | 'NEXT_WORKFLOW_CONSISTENCY'
  | 'DECISION_HAS_BRANCHES'
  | 'TRANSFORMATION_HAS_NEXT'
  | 'TRANSFORMATION_HAS_ENTRIES'
  | 'WAIT_HAS_NEXT'
  | 'PARALLEL_MIN_BRANCHES'
  | 'PARALLEL_HAS_JOIN'
  | 'JOIN_HAS_NEXT'
  | 'REF_RESOLVES'
  | 'ALL_REACHABLE'
  | 'END_REACHABLE'
  | 'BOUNDARY_TYPE'
  | 'BOUNDARY_DURATION'
  | 'BOUNDARY_TARGET_RESOLVES'
  | 'BOUNDARY_PARENT_SUPPORTS'
  | 'NO_NESTED_PARALLEL'
  | 'DECISION_EXPR_SYNTAX'
  | 'DECISION_EXPR_NON_BOOLEAN'
  | 'DECISION_EXPR_UNKNOWN_IDENT'
  | 'DECISION_EXPR_REFS'
  | 'TRANSFORMATION_EXPR_SYNTAX'
  | 'UNKNOWN_FIELDS_PRESENT'
  | 'GRAPH_CYCLE';

export interface Diagnostic {
  code: DiagnosticCode;
  severity: 'error' | 'warning';
  message: string;
  nodeId?: StepId;
  field?: string;
  branchKey?: string;
  boundaryIndex?: number;
}

export interface GraphNode {
  id: StepId;
  type: StepType;
  name: string;
  isEntry: boolean;
  step: Step;
}

export interface GraphEdge {
  id: string;
  from: StepId;
  to: StepId;
  variant: EdgeVariant;
}
