package boundary

import (
	"context"
	"time"
)

// DueEvent is one boundary timer that has fired (UPDATE…RETURNING already
// committed it as fired=true atomically — the dispatcher only needs the
// post-commit row).
type DueEvent struct {
	ID              string
	InstanceID      string
	StepExecutionID string
	TargetStepID    string
	Interrupting    bool
}

// BoundaryStore is the data-access contract for the boundary package.
type BoundaryStore interface {
	// FetchAndMarkFiredBoundaryEvents atomically marks unfired events with
	// fire_at <= now() as fired and returns them. Implemented as a single
	// UPDATE…RETURNING so the operation needs no caller-managed transaction.
	FetchAndMarkFiredBoundaryEvents(ctx context.Context) ([]DueEvent, error)

	// DeleteObsoleteBoundaryEvents removes rows that no longer serve any
	// operational purpose, in two cases:
	//   1. fired=true AND fire_at < now() - retention — the timer already
	//      fired (and was either applied or suppressed) more than retention
	//      ago; the retention window keeps recent rows for debugging.
	//   2. fired=false AND the parent step_execution has left RUNNING — the
	//      boundary will be suppressed at dispatch anyway, so the row is dead
	//      weight. No grace period: a terminal step never re-enters RUNNING.
	// Returns the number of rows deleted.
	DeleteObsoleteBoundaryEvents(ctx context.Context, retention time.Duration) (int64, error)
}
