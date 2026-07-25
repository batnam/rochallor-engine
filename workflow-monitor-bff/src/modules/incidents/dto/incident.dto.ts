export interface IncidentRow {
  id: string;
  instance_id: string;
  definition_id: string;
  definition_version: number;
  definition_name: string;
  step_id: string;
  step_type: string;
  attempt_number: number;
  occurred_at: Date;
  job_id: string | null;
  job_type: string | null;
  job_status: string | null;
}

export interface IncidentDetailRow extends IncidentRow {
  error_details: string | null;
  instance_status: string;
  business_key: string | null;
}

export interface IncidentListItem {
  id: string;
  processInstanceId: string;
  definitionId: string;
  definitionVersion: number;
  definitionName: string;
  stepId: string;
  stepType: string;
  attemptNumber: number;
  occurredAt: Date;
  job: {
    id: string;
    type: string;
    status: string;
  } | null;
}

export interface IncidentDetail extends IncidentListItem {
  errorDetails: string | null;
  processInstance: {
    id: string;
    status: string;
    businessKey: string | null;
  };
}

export interface IncidentFilters {
  definitionId?: string;
  jobType?: string;
  from?: string;
  to?: string;
}

export interface IncidentQuery extends IncidentFilters {
  cursor?: string;
  pageSize?: string;
}

export interface IncidentCursor {
  v: 1;
  occurredAt: string;
  id: string;
  filters: string;
}
