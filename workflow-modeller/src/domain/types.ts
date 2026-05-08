import type { z } from 'zod';
import type {
  zBoundaryEvent,
  zDecision,
  zDecisionTable,
  zDecisionTableRule,
  zDecisionTableStep,
  zEnd,
  zHitPolicy,
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
  | 'DECISION_TABLE'
  | 'TRANSFORMATION'
  | 'WAIT'
  | 'PARALLEL_GATEWAY'
  | 'JOIN_GATEWAY'
  | 'END';

export type StepId = string;

export type ServiceTaskStep = z.infer<typeof zServiceTask>;
export type UserTaskStep = z.infer<typeof zUserTask>;
export type DecisionStep = z.infer<typeof zDecision>;
export type DecisionTableStep = z.infer<typeof zDecisionTableStep>;
export type DecisionTable = z.infer<typeof zDecisionTable>;
export type DecisionTableRule = z.infer<typeof zDecisionTableRule>;
export type HitPolicy = z.infer<typeof zHitPolicy>;
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
  | 'GRAPH_CYCLE'
  | 'DECISION_TABLE_HAS_RULES'
  | 'DECISION_TABLE_HAS_NEXT'
  | 'DECISION_TABLE_HIT_POLICY_UNKNOWN'
  | 'DECISION_TABLE_AGGREGATOR_ON_NON_C'
  | 'DECISION_TABLE_LEGACY_THEN'
  | 'DECISION_TABLE_LEGACY_DEFAULT_NEXT_STEP'
  | 'DECISION_TABLE_UNREACHABLE_RULE'
  | 'DT_CELL_EXPR_SYNTAX'
  | 'DT_CELL_EXPR_NON_BOOLEAN'
  | 'DT_CELL_EXPR_UNKNOWN_IDENT'
  | 'DT_OUTPUT_EXPR_SYNTAX'
  | 'STEP_FIELD_INVALID';

export interface Diagnostic {
  code: DiagnosticCode;
  severity: 'error' | 'warning' | 'info';
  message: string;
  nodeId?: StepId;
  field?: string;
  branchKey?: string;
  boundaryIndex?: number;
  ruleIndex?: number;
  cellColumn?: string;
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
  sourceHandle?: string;
}
