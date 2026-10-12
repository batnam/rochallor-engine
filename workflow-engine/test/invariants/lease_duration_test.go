//go:build integration

package invariants_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLeaseUsesDatabaseClock(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	// The test bubble skews only the caller's Go clock to 2000. PostgreSQL
	// and its existing pool stay on the real clock; no host clock is changed.
	synctest.Test(t, func(t *testing.T) {
		if time.Now().Year() != 2000 {
			t.Fatal("test clock was not skewed")
		}
		jobs, err := gJobStore.PollJobs(context.Background(), "clock-worker", []string{f.jobType}, 1, 7)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("poll: %v, count=%d", err, len(jobs))
		}
		if jobs[0].LockExpiresAt == nil || jobs[0].LockedAt == nil {
			t.Fatal("missing timestamps")
		}
		lease := jobs[0].LockExpiresAt.Sub(*jobs[0].LockedAt)
		if lease < 7*time.Second || lease > 7*time.Second+time.Millisecond {
			t.Fatalf("lease used caller's skewed clock: %s", lease)
		}
	})
}

func TestLeaseStartsAfterWaitingForPool(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	config := gPool.Config()
	config.MaxConns, config.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	held, err := pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	store := pgstore.NewJobStore(pool)
	results := make(chan []instance.Job, 1)
	errs := make(chan error, 1)
	go func() {
		jobs, err := store.PollJobs(f.ctx, "pool-worker", []string{f.jobType}, 1, 5)
		results <- jobs
		errs <- err
	}()
	// Keep the sole connection occupied long enough to distinguish an expiry
	// computed before pool acquisition from a full five seconds at DB claim.
	time.Sleep(400 * time.Millisecond)
	held.Release()
	jobs := <-results
	if err := <-errs; err != nil || len(jobs) != 1 {
		t.Fatalf("poll: %v, count=%d", err, len(jobs))
	}
	var remaining float64
	if err := gPool.QueryRow(f.ctx, `SELECT extract(epoch FROM lock_expires_at-clock_timestamp()) FROM job WHERE id=$1`, jobs[0].ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 4.85 || remaining > 5 {
		t.Fatalf("lease shortened while waiting for connection: %.3fs", remaining)
	}
}

func TestLeaseConcurrentClaimAndCustomDuration(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	var wg sync.WaitGroup
	jobs := make(chan instance.Job, 10)
	for range 10 {
		wg.Go(func() {
			claimed, err := gJobStore.PollJobs(f.ctx, "concurrent-worker", []string{f.jobType}, 1, 60)
			if err != nil {
				t.Error(err)
				return
			}
			for _, j := range claimed {
				jobs <- j
			}
		})
	}
	wg.Wait()
	close(jobs)
	if len(jobs) != 1 {
		t.Fatalf("expected exactly one claim, got %d", len(jobs))
	}
	j := <-jobs
	if got := j.LockExpiresAt.Sub(*j.LockedAt); got < 60*time.Second || got > 60*time.Second+time.Millisecond {
		t.Fatalf("custom lease: %s", got)
	}
}
