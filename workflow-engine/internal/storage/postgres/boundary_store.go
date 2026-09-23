package postgres

import (
	"context"
	"fmt"
	"time"

	"errors"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/jackc/pgx/v5"
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

func (s *BoundaryStore) ListDueBoundaryEvents(ctx context.Context) ([]boundary.DueEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, instance_id, step_execution_id, target_step_id, interrupting
        FROM boundary_event_schedule WHERE fired = false AND fire_at <= now()
        ORDER BY fire_at, id`)
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

// The caller holds the instance lock before claiming the timer, matching all
// callback and interruption paths. A rollback leaves the timer pending.
func (s *InstanceStore) ClaimDueBoundaryEvent(ctx context.Context, tx db.Tx, eventID, instanceID string) (*boundary.DueEvent, error) {
	var e boundary.DueEvent
	err := Unwrap(tx).QueryRow(ctx, `UPDATE boundary_event_schedule SET fired=true
 WHERE id=$1 AND instance_id=$2 AND fired=false AND fire_at<=now()
 RETURNING id, instance_id, step_execution_id, target_step_id, interrupting`, eventID, instanceID).
		Scan(&e.ID, &e.InstanceID, &e.StepExecutionID, &e.TargetStepID, &e.Interrupting)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim boundary timer: %w", err)
	}
	return &e, nil
}
