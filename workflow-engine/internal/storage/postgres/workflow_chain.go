package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/jackc/pgx/v5"
)

func (s *InstanceStore) InsertWorkflowChain(ctx context.Context, tx db.Tx, request instance.WorkflowChain) error {
	_, err := Unwrap(tx).Exec(ctx, `INSERT INTO workflow_chain(source_instance_id,target_definition_id,variables,business_key)
 VALUES($1,$2,$3,$4) ON CONFLICT(source_instance_id) DO NOTHING`, request.SourceInstanceID, request.TargetDefinitionID, request.Variables, request.BusinessKey)
	if err != nil {
		return fmt.Errorf("insert workflow chain: %w", err)
	}
	return nil
}

func (s *InstanceStore) ListPendingWorkflowChains(ctx context.Context) ([]instance.WorkflowChain, error) {
	rows, err := s.pool.Query(ctx, `SELECT source_instance_id,target_definition_id,variables,business_key
 FROM workflow_chain WHERE child_instance_id IS NULL ORDER BY created_at,source_instance_id`)
	if err != nil {
		return nil, fmt.Errorf("list workflow chains: %w", err)
	}
	defer rows.Close()
	var requests []instance.WorkflowChain
	for rows.Next() {
		var r instance.WorkflowChain
		if err := rows.Scan(&r.SourceInstanceID, &r.TargetDefinitionID, &r.Variables, &r.BusinessKey); err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

func (s *InstanceStore) ClaimWorkflowChain(ctx context.Context, tx db.Tx, sourceInstanceID string) (bool, error) {
	var id string
	err := Unwrap(tx).QueryRow(ctx, `SELECT source_instance_id FROM workflow_chain
 WHERE source_instance_id=$1 AND child_instance_id IS NULL FOR UPDATE SKIP LOCKED`, sourceInstanceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *InstanceStore) CompleteWorkflowChain(ctx context.Context, tx db.Tx, sourceInstanceID, childInstanceID string) error {
	_, err := Unwrap(tx).Exec(ctx, `UPDATE workflow_chain SET child_instance_id=$2 WHERE source_instance_id=$1`, sourceInstanceID, childInstanceID)
	return err
}
