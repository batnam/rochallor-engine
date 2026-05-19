package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func validateApplication(ctx context.Context, job handler.JobContext) (handler.Result, error) {
	applicationID, ok := job.Variables["applicationId"].(string)

	slog.Info("##### validateApplication called", "applicationId", applicationID)
	if !ok || applicationID == "" {
		return handler.Result{}, &retry.NonRetryable{
			Cause: errors.New("missing applicationId"),
		}
	}

	// TODO call to validation service
	// e.g err := validationService.Validate(ctx, applicationID)

	return handler.Result{
		VariablesToSet: map[string]any{
			"validationPassed": true,
		},
	}, nil

}
