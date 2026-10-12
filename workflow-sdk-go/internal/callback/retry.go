// Package callback retries an already computed handler outcome without running
// the handler again. Cancellation ends the delivery obligation for this owner.
package callback

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/metrics"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const AttemptTimeout = 5 * time.Second

func Retry(ctx context.Context, mode, kind string, log *slog.Logger, send func(context.Context) error) error {
	return deliver(ctx, mode, kind, log, send, true)
}

// Once keeps legacy clients without lease metadata to a single bounded attempt.
func Once(ctx context.Context, mode, kind string, log *slog.Logger, send func(context.Context) error) error {
	return deliver(ctx, mode, kind, log, send, false)
}

func deliver(ctx context.Context, mode, kind string, log *slog.Logger, send func(context.Context) error, repeat bool) error {
	finish := metrics.TrackCallback(mode, kind)
	defer finish()
	start := time.Now()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, AttemptTimeout)
		err := send(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if Permanent(err) {
			metrics.CallbackRejected.WithLabelValues(mode, kind).Inc()
			log.Error("callback rejected; delivery remains unacknowledged", "callback", kind, "err", err)
			return err
		}
		if !repeat {
			return err
		}
		metrics.CallbackRetries.WithLabelValues(mode, kind).Inc()
		log.Warn("callback pending", "callback", kind, "attempt", attempt, "age_seconds", time.Since(start).Seconds(), "err", err)
		timer := time.NewTimer(retry.Delay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Permanent classifies protocol errors that retrying an unchanged request cannot fix.
func Permanent(err error) bool {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 408 && httpErr.StatusCode != 429
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied,
		codes.FailedPrecondition, codes.OutOfRange, codes.Unimplemented, codes.Unauthenticated, codes.DataLoss:
		return true
	}
	return false
}
