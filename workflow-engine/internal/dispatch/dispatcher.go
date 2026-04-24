// Package dispatch provides the engine-internal seam that decides HOW a newly
// created job is made visible to workers. It does NOT decide whether the job
// is created — instance.Service does that.
//
// Two implementations exist:
//   - dispatch/polling:      no-op. The job row itself is the signal for the
//                            existing FOR UPDATE SKIP LOCKED poll path (FR-001, FR-009).
//   - dispatch/kafka_outbox: writes a dispatch_outbox row in the same tx; a
//                            leader-elected relay drains those rows to Kafka
//                            (FR-002, FR-003, FR-017, FR-018).
//
// cmd/engine/main.go selects one implementation at startup based on
// cfg.DispatchMode and wires it through instance.NewService.
package dispatch

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DispatchJob is the narrow subset of job fields the dispatcher needs at
// enqueue time. Defining it here (rather than referencing instance.Job)
// keeps the dependency graph one-way: instance → dispatch.
//
// The field set aligns with workflow.v1.JobDispatchEvent (FR-016); the
// kafka_outbox Enqueue serializes these into that proto.
type DispatchJob struct {
	ID               string
	InstanceID       string
	StepExecutionID  string
	JobType          string
	RetriesRemaining int
	// Payload is the JSON-encoded job input (mirrors the existing `job.payload`
	// JSONB column, which is how polling workers already receive it).
	Payload   []byte
	CreatedAt time.Time
}

// Dispatcher is called by instance.Service once per newly-created job, INSIDE
// the transaction that inserts the job row (FR-002, INV-1).
//
// Enqueue MUST be synchronous, MUST participate in the caller's transaction
// (no nested transaction, no side-effects outside tx), and MUST NOT touch the
// network. Any network activity belongs to the relay goroutine, which runs
// separately and operates on committed outbox rows.
type Dispatcher interface {
	Enqueue(ctx context.Context, tx pgx.Tx, job DispatchJob) error
}

// Runtime is the per-mode startup/shutdown lifecycle. For polling mode it is
// a no-op; for kafka_outbox mode it owns the broker client, the advisory-lock
// leader-election loop, and the relay goroutine.
type Runtime interface {
	// Start opens the broker connection (if any), validates dependencies
	// (FR-008), and — in kafka_outbox mode — starts the relay goroutine.
	// Returns a non-nil error if startup validation fails, causing the
	// engine to exit with a loud failure. No silent fallback.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the relay, releases the advisory lock if
	// held, and closes the broker client. Safe to call multiple times.
	Stop(ctx context.Context) error

	// Dispatcher returns the hot-path Dispatcher wired for this mode.
	// The returned value is stable for the lifetime of the Runtime.
	Dispatcher() Dispatcher
}
