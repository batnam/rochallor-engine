import { z } from 'zod';

const zJsonValue: z.ZodType = z.lazy(() =>
  z.union([
    z.string(),
    z.number(),
    z.boolean(),
    z.null(),
    z.array(zJsonValue),
    z.record(zJsonValue),
  ]),
);

export const zBoundaryEvent = z
  .object({
    type: z.literal('TIMER'),
    duration: z.string().min(1),
    interrupting: z.boolean(),
    targetStepId: z.string().min(1),
  })
  .passthrough();

const stepCommon = {
  id: z.string(),
  name: z.string(),
  description: z.string().optional(),
};

export const zServiceTask = z
  .object({
    ...stepCommon,
    type: z.literal('SERVICE_TASK'),
    nextStep: z.string().optional(),
    jobType: z.string().optional(),
    delegateClass: z.string().optional(),
    retryCount: z.number().int().nonnegative().optional(),
    boundaryEvents: z.array(zBoundaryEvent).optional(),
  })
  .passthrough();

export const zUserTask = z
  .object({
    ...stepCommon,
    type: z.literal('USER_TASK'),
    nextStep: z.string().optional(),
    jobType: z.string().optional(),
    boundaryEvents: z.array(zBoundaryEvent).optional(),
  })
  .passthrough();

export const zDecision = z
  .object({
    ...stepCommon,
    type: z.literal('DECISION'),
    conditionalNextSteps: z.record(z.string()),
  })
  .passthrough();

/**
 * Hit policy codes for DECISION_TABLE steps (007 wire format).
 *
 * - U  Unique:     at most one rule may match; multiple matches → engine fails the step.
 * - F  First:      first matching rule in document order wins.
 * - A  Any:        every matching rule must produce structurally-equal outputs.
 * - R  Rule Order: every matching rule contributes; per-column lists in document order.
 * - C  Collect:    every matching rule contributes; per-column lists (no order promised).
 * - C+ / C# / C> / C< : Collect with aggregator (sum / count / max / min) — scalar per column.
 *
 * Omitted/empty defaults to "U" (Unique) at engine runtime; the modeller's
 * editor pre-selects "U" so the author still has a single click to accept it.
 * See specs/007-decision-table-outputs/data-model.md §1.
 */
export const zHitPolicy = z.enum(['U', 'F', 'A', 'R', 'C', 'C+', 'C#', 'C>', 'C<']);

export const zDecisionTableRule = z
  .object({
    when: z.record(z.string()).default({}),
    outputs: z.record(zJsonValue).default({}),
  })
  .passthrough();

export const zDecisionTable = z
  .object({
    rules: z.array(zDecisionTableRule).min(1),
  })
  .passthrough();

export const zDecisionTableStep = z
  .object({
    ...stepCommon,
    type: z.literal('DECISION_TABLE'),
    hitPolicy: zHitPolicy.optional().default('U'),
    nextStep: z.string().min(1),
    decisionTable: zDecisionTable,
  })
  .passthrough();

export const zTransformation = z
  .object({
    ...stepCommon,
    type: z.literal('TRANSFORMATION'),
    nextStep: z.string(),
    transformations: z.record(zJsonValue),
  })
  .passthrough();

export const zWait = z
  .object({
    ...stepCommon,
    type: z.literal('WAIT'),
    nextStep: z.string(),
    boundaryEvents: z.array(zBoundaryEvent).optional(),
  })
  .passthrough();

export const zParallelGateway = z
  .object({
    ...stepCommon,
    type: z.literal('PARALLEL_GATEWAY'),
    parallelNextSteps: z.array(z.string()),
    joinStep: z.string(),
  })
  .passthrough();

export const zJoinGateway = z
  .object({
    ...stepCommon,
    type: z.literal('JOIN_GATEWAY'),
    nextStep: z.string(),
  })
  .passthrough();

export const zEnd = z
  .object({
    ...stepCommon,
    type: z.literal('END'),
  })
  .passthrough();

export const zStep = z.discriminatedUnion('type', [
  zServiceTask,
  zUserTask,
  zDecision,
  zDecisionTableStep,
  zTransformation,
  zWait,
  zParallelGateway,
  zJoinGateway,
  zEnd,
]);

export const zWorkflowDefinition = z
  .object({
    id: z.string(),
    version: z.number().optional(),
    name: z.string(),
    description: z.string().optional(),
    autoStartNextWorkflow: z.boolean().optional(),
    nextWorkflowId: z.string().optional(),
    steps: z.array(zStep),
    metadata: z.record(zJsonValue).optional(),
  })
  .passthrough();

export const STEP_TYPES = [
  'SERVICE_TASK',
  'USER_TASK',
  'DECISION',
  'DECISION_TABLE',
  'TRANSFORMATION',
  'WAIT',
  'PARALLEL_GATEWAY',
  'JOIN_GATEWAY',
  'END',
] as const;

/** Keys the contract enumerates per variant — anything else is a passthrough unknown. */
export const KNOWN_STEP_KEYS: Record<(typeof STEP_TYPES)[number], readonly string[]> = {
  SERVICE_TASK: [
    'id',
    'name',
    'type',
    'description',
    'nextStep',
    'jobType',
    'delegateClass',
    'retryCount',
    'boundaryEvents',
  ],
  USER_TASK: ['id', 'name', 'type', 'description', 'nextStep', 'jobType', 'boundaryEvents'],
  DECISION: ['id', 'name', 'type', 'description', 'conditionalNextSteps'],
  DECISION_TABLE: ['id', 'name', 'type', 'description', 'hitPolicy', 'nextStep', 'decisionTable'],
  TRANSFORMATION: ['id', 'name', 'type', 'description', 'nextStep', 'transformations'],
  WAIT: ['id', 'name', 'type', 'description', 'nextStep', 'boundaryEvents'],
  PARALLEL_GATEWAY: ['id', 'name', 'type', 'description', 'parallelNextSteps', 'joinStep'],
  JOIN_GATEWAY: ['id', 'name', 'type', 'description', 'nextStep'],
  END: ['id', 'name', 'type', 'description'],
};

export const KNOWN_ROOT_KEYS = [
  'id',
  'version',
  'name',
  'description',
  'autoStartNextWorkflow',
  'nextWorkflowId',
  'steps',
  'metadata',
] as const;
