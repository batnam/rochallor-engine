package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/boundary"
)

// BoundaryStore implements boundary.BoundaryStore.
type BoundaryStore struct {
	pool *pgxpool.Pool
}

// NewBoundaryStore returns a boundary.BoundaryStore backed by pool.
func NewBoundaryStore(pool *pgxpool.Pool) boundary.BoundaryStore {
	return &BoundaryStore{pool: pool}
}

func (s *BoundaryStore) FetchAndMarkFiredBoundaryEvents(ctx context.Context) ([]boundary.DueEvent, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE boundary_event_schedule
		SET    fired = true
		WHERE  fired = false AND fire_at <= now()
		RETURNING id, instance_id, step_execution_id, target_step_id, interrupting`)
	if err != nil {
		return nil, fmt.Errorf("fetch due boundary events: %w", err)
	}
	defer rows.Close()

	var due []boundary.DueEvent
	for rows.Next() {
		var e boundary.DueEvent
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.StepExecutionID, &e.TargetStepID, &e.Interrupting); err != nil {
			return nil, fmt.Errorf("scan boundary event: %w", err)
		}
		due = append(due, e)
	}
	return due, rows.Err()
}

// Compile-time interface assertion.
var _ boundary.BoundaryStore = (*BoundaryStore)(nil)
