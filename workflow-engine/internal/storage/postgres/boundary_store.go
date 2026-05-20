package postgres

import (
	"context"
	"fmt"
	"time"

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

func (s *BoundaryStore) DeleteObsoleteBoundaryEvents(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM boundary_event_schedule b
		 WHERE (b.fired = true AND b.fire_at < now() - $1::interval)
		    OR (b.fired = false AND EXISTS (
		           SELECT 1 FROM step_execution se
		           WHERE se.id = b.step_execution_id AND se.status <> 'RUNNING'
		       ))`,
		retention,
	)
	if err != nil {
		return 0, fmt.Errorf("delete obsolete boundary events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Compile-time interface assertion.
var _ boundary.BoundaryStore = (*BoundaryStore)(nil)
