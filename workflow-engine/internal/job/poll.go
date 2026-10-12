// Package job implements the job poll/complete/fail/retry lifecycle and the
// background lease-expiry sweeper. Workers interact with the Engine
// exclusively via the functions in this package.
package job

import (
	"context"
	"errors"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// DefaultLeaseSeconds is the default lease length for a polled job
// (kept identical to the historical 30-second value so worker behaviour is
// unchanged).
const DefaultLeaseSeconds = 30

var ErrInvalidLeaseSeconds = errors.New("leaseSeconds must be 0 (default 30) or between 5 and 3600")

func LeaseSeconds(seconds int) (int, error) {
	if seconds == 0 {
		return DefaultLeaseSeconds, nil
	}
	if seconds < 5 || seconds > 3600 {
		return 0, ErrInvalidLeaseSeconds
	}
	return seconds, nil
}

// Poll claims up to max unlocked jobs of the given jobTypes for workerID via
// the JobStore's atomic UPDATE…FOR UPDATE SKIP LOCKED so concurrent pollers
// never hand out the same job.
func Poll(ctx context.Context, store JobStore, workerID string, jobTypes []string, max int) ([]instance.Job, error) {
	return PollWithLease(ctx, store, workerID, jobTypes, max, 0)
}

// PollWithLease preserves Poll's default while allowing an explicit lease.
func PollWithLease(ctx context.Context, store JobStore, workerID string, jobTypes []string, max, seconds int) ([]instance.Job, error) {
	seconds, err := LeaseSeconds(seconds)
	if err != nil {
		return nil, err
	}
	return store.PollJobs(ctx, workerID, jobTypes, max, seconds)
}
