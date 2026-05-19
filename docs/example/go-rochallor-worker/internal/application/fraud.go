package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func fraudScreen(ctx context.Context, job handler.JobContext) (handler.Result, error) {
	applicationID, ok := job.Variables["applicationId"].(string)

	slog.Info("#### fraudScreen called", "applicationId", applicationID)
	if !ok || applicationID == "" {
		return handler.Result{}, &retry.NonRetryable{
			Cause: errors.New("missing applicationId"),
		}
	}

	// TODO call to fraud service

	return handler.Result{
		VariablesToSet: map[string]any{
			"fraudScreened": true,
			"fraudScore":    0,
		},
	}, nil

}
