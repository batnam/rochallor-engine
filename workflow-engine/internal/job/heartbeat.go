package job

import (
	"context"
	"errors"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

var (
	ErrLeaseLost         = errors.New("job does not have an active lease for this worker")
	ErrInvalidLeaseOwner = errors.New("jobId and workerId are required")
)

// ExtendLease serializes with callbacks/recovery under instance-then-job locks.
// The job ID is the attempt fence; retired attempts cannot be revived.
func ExtendLease(ctx context.Context, dbConn db.DB, store JobStore, jobID, workerID string, seconds int) (time.Time, error) {
	if jobID == "" || workerID == "" {
		return time.Time{}, ErrInvalidLeaseOwner
	}
	seconds, err := LeaseSeconds(seconds)
	if err != nil {
		return time.Time{}, err
	}
	var expiry time.Time
	err = dbConn.RunInTx(ctx, "job.heartbeat", func(tx db.Tx) error {
		var accepted bool
		var err error
		expiry, accepted, err = store.ExtendJobLease(ctx, tx, jobID, workerID, seconds)
		if err != nil {
			return err
		}
		if !accepted {
			return ErrLeaseLost
		}
		return nil
	})
	result := "accepted"
	if errors.Is(err, ErrLeaseLost) {
		result = "lease_lost"
	} else if err != nil {
		result = "error"
	}
	obs.JobHeartbeatTotal.WithLabelValues(result).Inc()
	return expiry, err
}
