// Package job implements the job poll/complete/fail/retry lifecycle and the
// background lease-expiry sweeper. Workers interact with the Engine
// exclusively via the functions in this package.
package job

import (
	"context"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// defaultLockDurationSeconds is the default lease length for a polled job
// (kept identical to the historical 30-second value so worker behaviour is
// unchanged).
const defaultLockDurationSeconds = 30

// Poll claims up to max unlocked jobs of the given jobTypes for workerID via
// the JobStore's atomic UPDATE…FOR UPDATE SKIP LOCKED so concurrent pollers
// never hand out the same job.
func Poll(ctx context.Context, store JobStore, workerID string, jobTypes []string, max int) ([]instance.Job, error) {
	return store.PollJobs(ctx, workerID, jobTypes, max, defaultLockDurationSeconds)
}
