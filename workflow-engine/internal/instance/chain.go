package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// WorkflowChain is a durable request to start a child from a completed parent.
// Variables and business key are the parent's snapshot at completion.
type WorkflowChain struct {
	SourceInstanceID   string
	TargetDefinitionID string
	Variables          json.RawMessage
	BusinessKey        *string
}

// StartChainWorker retries pending requests until shutdown. No work depends on
// an in-memory goroutine surviving a parent transaction or an engine restart.
func (s *Service) StartChainWorker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.ProcessPendingChains(ctx); err != nil {
					slog.Error("workflow chain: pending work failed; will retry", "err", err)
				}
			}
		}
	}()
}

// ProcessPendingChains processes a snapshot of committed requests. A failed
// request stays pending and does not prevent other parents from starting children.
func (s *Service) ProcessPendingChains(ctx context.Context) error {
	requests, err := s.store.ListPendingWorkflowChains(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, request := range requests {
		if err := s.startChainedWorkflow(ctx, request); err != nil {
			slog.Error("workflow chain: start failed", "source_instance_id", request.SourceInstanceID, "target_definition_id", request.TargetDefinitionID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Service) startChainedWorkflow(ctx context.Context, request WorkflowChain) error {
	variables, err := variablesToMap(request.Variables)
	if err != nil {
		return err
	}
	// Resolve the latest definition at delivery time, as Start(...,version=0)
	// does. Lookup/schema failures leave the request available for a later retry.
	def, varJSON, err := s.prepareStart(ctx, request.TargetDefinitionID, 0, variables)
	if err != nil {
		return fmt.Errorf("chain %s: %w", request.SourceInstanceID, err)
	}
	return s.db.RunInTx(ctx, "instance.start_chain", func(tx db.Tx) error {
		claimed, err := s.store.ClaimWorkflowChain(ctx, tx, request.SourceInstanceID)
		if err != nil || !claimed {
			return err
		}
		var businessKey string
		if request.BusinessKey != nil {
			businessKey = *request.BusinessKey
		}
		child, err := s.startInTx(ctx, tx, def, varJSON, businessKey)
		if err != nil {
			return err
		}
		return s.store.CompleteWorkflowChain(ctx, tx, request.SourceInstanceID, child.ID)
	})
}
