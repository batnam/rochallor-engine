package disbursment

import (
	"context"
	"errors"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func notifyDisbursement(ctx context.Context, job handler.JobContext) (handler.Result, error) {
	applicationID, appIdOK := job.Variables["applicationId"].(string)
	customerID, cusIdOK := job.Variables["customerId"].(string)

	slog.Info("##### notifyDisbursement called", "applicationId", "customerID", applicationID, customerID)

	if !appIdOK || !cusIdOK || applicationID == "" || customerID == "" {
		return handler.Result{}, &retry.NonRetryable{
			Cause: errors.New("missing applicationId or customerId"),
		}
	}

	// TODO Call to notify service

	return handler.Result{
		VariablesToSet: map[string]any{
			"disbursementNotified": true,
		},
	}, nil
}

func notifyApprovalOverdue(ctx context.Context, job handler.JobContext) (handler.Result, error) {
	applicationID, appIdOK := job.Variables["applicationId"].(string)
	customerID, cusIdOK := job.Variables["customerId"].(string)

	slog.Info("##### notifyApprovalOverdue called", "applicationId", "customerID", applicationID, customerID)

	if !appIdOK || !cusIdOK || applicationID == "" || customerID == "" {
		return handler.Result{}, &retry.NonRetryable{
			Cause: errors.New("missing applicationId or customerId"),
		}
	}

	// TODO Call to notify service

	return handler.Result{
		VariablesToSet: map[string]any{
			"approvalOverdueNotified": true,
		},
	}, nil
}
