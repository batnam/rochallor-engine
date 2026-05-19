package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func creditScore(ctx context.Context, job handler.JobContext) (handler.Result, error) {
	applicationID, ok := job.Variables["applicationId"].(string)
	customerId, okCus := job.Variables["customerId"].(string)

	slog.Info("##### creditScore called", "applicationId", applicationID)
	if !ok || !okCus || applicationID == "" || customerId == "" {
		return handler.Result{}, &retry.NonRetryable{
			Cause: errors.New("missing applicationId or customerId"),
		}
	}

	// TODO call to validation service
	// e.g err := validationService.Validate(ctx, applicationID)
	var creditScoreValue = 1000
	if customerId == "123456789" {
		creditScoreValue = 649
	}
	return handler.Result{
		VariablesToSet: map[string]any{
			"creditScoreChecked": true,
			"creditScore":        creditScoreValue,
		},
	}, nil

}
