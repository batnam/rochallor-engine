export interface ProcessInstanceRow {
  id: string;
  definition_id: string;
  definition_version: number;
  status: string;
  current_step_ids: string[];
  started_at: Date;
  completed_at: Date | null;
  failure_reason: string | null;
  business_key: string | null;
}

export interface ProcessInstanceListItem {
  id: string;
  definitionId: string;
  definitionVersion: number;
  status: string;
  currentStepIds: string[];
  startedAt: Date;
  completedAt: Date | null;
  failureReason: string | null;
  businessKey: string | null;
}

export interface WorkflowDefinitionDocument {
  id: string;
  version: number;
  name: string;
  steps: unknown[];
  [key: string]: unknown;
}

export interface StepExecutionSummary {
  executionId: string;
  stepId: string;
  status: string;
  attemptNumber: number;
}

export interface StepExecutionListItem {
  id: string;
  stepId: string;
  stepType: string;
  attemptNumber: number;
  status: string;
  startedAt: Date;
  endedAt: Date | null;
  hasFailure: boolean;
  hasInputSnapshot: boolean;
  hasOutputSnapshot: boolean;
}

export interface ProcessInstanceDetail {
  instance: ProcessInstanceListItem;
  definition: WorkflowDefinitionDocument;
  executionOverlay: {
    currentTokenStepIds: string[];
    failedStepId: string | null;
    latestByStep: StepExecutionSummary[];
  };
}

export interface ProcessInstanceFilters {
  definitionId?: string;
  status?: string | string[];
  businessKey?: string;
  from?: string;
  to?: string;
}

export interface ProcessInstanceQuery extends ProcessInstanceFilters {
  cursor?: string;
  pageSize?: string;
}

export interface ProcessInstanceCursor {
  v: 1;
  startedAt: string;
  id: string;
  filters: string;
}

export interface StepExecutionCursor {
  v: 1;
  startedAt: string;
  id: string;
  instanceId: string;
}

export interface StepExecutionQuery {
  cursor?: string;
  pageSize?: string;
}
