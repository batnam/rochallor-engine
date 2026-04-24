// Package instance provides Go struct representations of the runtime entities
// persisted in PostgreSQL (data-model §2). These types mirror the database columns
// one-for-one so that storage queries can scan directly into them without
// intermediate mapping layers.
package instance

import (
	"encoding/json"
	"time"
)

// InstanceStatus is the lifecycle state of a workflow instance.
type InstanceStatus string

const (
	InstanceStatusActive    InstanceStatus = "ACTIVE"
	InstanceStatusWaiting   InstanceStatus = "WAITING"
	InstanceStatusCompleted InstanceStatus = "COMPLETED"
	InstanceStatusFailed    InstanceStatus = "FAILED"
	InstanceStatusCancelled InstanceStatus = "CANCELLED"
)

// StepExecutionStatus is the lifecycle state of a single step execution row.
type StepExecutionStatus string

const (
	StepExecutionStatusRunning   StepExecutionStatus = "RUNNING"
	StepExecutionStatusCompleted StepExecutionStatus = "COMPLETED"
	StepExecutionStatusFailed    StepExecutionStatus = "FAILED"
	StepExecutionStatusSkipped   StepExecutionStatus = "SKIPPED"
)

// JobStatus is the lifecycle state of a job row.
type JobStatus string

const (
	JobStatusUnlocked  JobStatus = "UNLOCKED"
	JobStatusLocked    JobStatus = "LOCKED"
	JobStatusCompleted JobStatus = "COMPLETED"
	JobStatusFailed    JobStatus = "FAILED"
)

// UserTaskStatus is the lifecycle state of a user task row.
type UserTaskStatus string

const (
	UserTaskStatusOpen      UserTaskStatus = "OPEN"
	UserTaskStatusCompleted UserTaskStatus = "COMPLETED"
	UserTaskStatusCancelled UserTaskStatus = "CANCELLED"
)

// WorkflowInstance is the runtime record for one execution of a workflow definition.
// Mirrors the workflow_instance table (data-model §2.2).
type WorkflowInstance struct {
	ID                string          `db:"id"                 json:"id"`
	DefinitionID      string          `db:"definition_id"      json:"definitionId"`
	DefinitionVersion int             `db:"definition_version" json:"definitionVersion"`
	Status            InstanceStatus  `db:"status"             json:"status"`
	CurrentStepIDs    []string        `db:"current_step_ids"   json:"currentStepIds"`
	Variables         json.RawMessage `db:"variables"          json:"variables,omitempty"`
	StartedAt         time.Time       `db:"started_at"         json:"startedAt"`
	CompletedAt       *time.Time      `db:"completed_at"       json:"completedAt,omitempty"`
	FailureReason     *string         `db:"failure_reason"     json:"failureReason,omitempty"`
	BusinessKey       *string         `db:"business_key"       json:"businessKey,omitempty"`
}

// StepExecution records a single entry into a step.
// One row per execution attempt; retries and parallel branches each create a new row.
// Mirrors the step_execution table (data-model §2.3).
type StepExecution struct {
	ID              string              `db:"id"`
	InstanceID      string              `db:"instance_id"`
	StepID          string              `db:"step_id"`
	StepType        string              `db:"step_type"`
	AttemptNumber   int                 `db:"attempt_number"`
	Status          StepExecutionStatus `db:"status"`
	StartedAt       time.Time           `db:"started_at"`
	EndedAt         *time.Time          `db:"ended_at"`
	InputSnapshot   json.RawMessage     `db:"input_snapshot"`
	OutputSnapshot  json.RawMessage     `db:"output_snapshot"`
	FailureReason   *string             `db:"failure_reason"`
}

// Job is a unit of work dispatched to an external SDK worker.
// Mirrors the job table (data-model §2.4).
type Job struct {
	ID               string          `db:"id"                  json:"id"`
	InstanceID       string          `db:"instance_id"         json:"instanceId"`
	StepExecutionID  string          `db:"step_execution_id"   json:"stepExecutionId"`
	JobType          string          `db:"job_type"            json:"jobType"`
	Status           JobStatus       `db:"status"              json:"status"`
	WorkerID         *string         `db:"worker_id"           json:"workerId,omitempty"`
	LockedAt         *time.Time      `db:"locked_at"           json:"lockedAt,omitempty"`
	LockExpiresAt    *time.Time      `db:"lock_expires_at"     json:"lockExpiresAt,omitempty"`
	RetriesRemaining int             `db:"retries_remaining"   json:"retriesRemaining"`
	Payload          json.RawMessage `db:"payload"             json:"variables,omitempty"`
	CreatedAt        time.Time       `db:"created_at"          json:"createdAt"`
}

// UserTask is a human-task row created at USER_TASK steps.
// Mirrors the user_task table (data-model §2.5).
type UserTask struct {
	ID              string          `db:"id"`
	InstanceID      string          `db:"instance_id"`
	StepExecutionID string          `db:"step_execution_id"`
	StepID          string          `db:"step_id"`
	Status          UserTaskStatus  `db:"status"`
	Assignee        *string         `db:"assignee"`
	AssigneeGroup   *string         `db:"assignee_group"`
	Payload         json.RawMessage `db:"payload"`
	Result          json.RawMessage `db:"result"`
	CreatedAt       time.Time       `db:"created_at"`
	CompletedAt     *time.Time      `db:"completed_at"`
}

// BoundaryEventSchedule tracks a pending TIMER boundary event.
// Mirrors the boundary_event_schedule table (data-model §2.6).
type BoundaryEventSchedule struct {
	ID              string    `db:"id"`
	InstanceID      string    `db:"instance_id"`
	StepExecutionID string    `db:"step_execution_id"`
	TargetStepID    string    `db:"target_step_id"`
	FireAt          time.Time `db:"fire_at"`
	Interrupting    bool      `db:"interrupting"`
	Fired           bool      `db:"fired"`
}

// AuditLogEntry is one row in the append-only audit_log table (data-model §2.7).
type AuditLogEntry struct {
	ID         int64           `db:"id"`
	At         time.Time       `db:"at"`
	Actor      string          `db:"actor"`
	Kind       string          `db:"kind"`
	InstanceID *string         `db:"instance_id"`
	Detail     json.RawMessage `db:"detail"`
}
