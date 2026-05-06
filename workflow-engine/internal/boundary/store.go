package boundary

import (
	"context"
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
}
