package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/internal/callback"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/metrics"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func (r *Runner) validateHeartbeat() error {
	if r.cfg.LeaseSeconds != 0 && (r.cfg.LeaseSeconds < 5 || r.cfg.LeaseSeconds > 3600) {
		return fmt.Errorf("LeaseSeconds must be 0 or 5–3600")
	}
	if !r.cfg.Heartbeat {
		return nil
	}
	if _, ok := r.engine.(client.LeaseExtender); !ok {
		return client.ErrHeartbeatUnsupported
	}
	if r.cfg.HeartbeatInterval < 0 || r.cfg.HeartbeatInterval > r.leaseDuration()/3 {
		return fmt.Errorf("HeartbeatInterval must be at most one third of the lease")
	}
	return nil
}

func (r *Runner) leaseDuration() time.Duration {
	seconds := r.cfg.LeaseSeconds
	if seconds == 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (r *Runner) leaseContext(parent context.Context, j client.Job) (context.Context, func(), error) {
	if !r.cfg.Heartbeat {
		if j.LockExpiresAt != nil {
			ctx, cancel := context.WithDeadline(parent, *j.LockExpiresAt)
			return ctx, cancel, nil
		}
		return parent, func() {}, nil
	}
	if err := r.validateHeartbeat(); err != nil {
		return nil, nil, err
	}
	if j.LockExpiresAt == nil {
		return nil, nil, fmt.Errorf("heartbeat requires lockExpiresAt lease metadata")
	}
	ctx, cancel := context.WithCancelCause(parent)
	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); r.maintainLease(ctx, cancel, j, ready) }()
	stop := func() { cancel(context.Canceled); <-done }
	select {
	case err := <-ready:
		if err != nil {
			stop()
			return nil, nil, err
		}
		return ctx, stop, nil
	case <-ctx.Done():
		stop()
		return nil, nil, context.Cause(ctx)
	}
}

// A single owner loop watches expiry even while an HTTP/RPC is in flight. It
// never starts a second heartbeat until the first returns or the attempt stops.
// Handler and immutable callback delivery share the cancellable attempt context.
func (r *Runner) maintainLease(ctx context.Context, cancel context.CancelCauseFunc, j client.Job, ready chan<- error) {
	expiry := *j.LockExpiresAt
	leaseTimer := time.NewTimer(max(0, time.Until(expiry)))
	defer leaseTimer.Stop()
	next := time.NewTimer(0) // capability/ownership acceptance before the handler starts
	defer next.Stop()
	type response struct {
		expiry time.Time
		err    error
	}
	var result <-chan response
	var stopCall context.CancelFunc
	defer func() {
		if stopCall != nil {
			stopCall()
		}
	}()
	initialized := false
	fail := func(err error) {
		cancel(err)
		if !initialized {
			ready <- err
			initialized = true
		}
		if !errors.Is(err, client.ErrLeaseLost) && !errors.Is(err, context.Canceled) {
			select {
			case r.stopErrors <- err:
			default:
			}
		}
	}
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-leaseTimer.C:
			metrics.Heartbeats.WithLabelValues("lease_lost").Inc()
			fail(client.ErrLeaseLost)
			return
		case <-next.C:
			if !time.Now().Before(expiry) {
				metrics.Heartbeats.WithLabelValues("lease_lost").Inc()
				fail(client.ErrLeaseLost)
				return
			}
			callCtx, stop := context.WithDeadline(ctx, minTime(expiry, time.Now().Add(callback.AttemptTimeout)))
			stopCall = stop
			ch := make(chan response, 1)
			result = ch
			go func() {
				until, err := r.engine.(client.LeaseExtender).ExtendJobLease(callCtx, j.ID, r.cfg.WorkerID, r.cfg.LeaseSeconds)
				ch <- response{until, err}
			}()
		case res := <-result:
			stopCall()
			stopCall = nil
			result = nil
			if ctx.Err() != nil {
				return
			}
			if !time.Now().Before(expiry) {
				metrics.Heartbeats.WithLabelValues("lease_lost").Inc()
				fail(client.ErrLeaseLost)
				return
			}
			if res.err == nil {
				if !res.expiry.After(time.Now()) || res.expiry.Before(expiry) {
					fail(fmt.Errorf("invalid heartbeat expiry"))
					return
				}
				expiry = res.expiry
				leaseTimer.Reset(time.Until(expiry))
				attempt = 0
				metrics.Heartbeats.WithLabelValues("accepted").Inc()
				if !initialized {
					ready <- nil
					initialized = true
				}
				interval := r.cfg.HeartbeatInterval
				if interval == 0 {
					interval = r.leaseDuration() / 3
				}
				next.Reset(min(interval, time.Until(expiry)/3))
			} else if errors.Is(res.err, client.ErrLeaseLost) {
				metrics.Heartbeats.WithLabelValues("lease_lost").Inc()
				fail(res.err)
				return
			} else if errors.Is(res.err, client.ErrHeartbeatUnsupported) || callback.Permanent(res.err) {
				metrics.Heartbeats.WithLabelValues("rejected").Inc()
				fail(res.err)
				return
			} else {
				attempt++
				metrics.Heartbeats.WithLabelValues("retry").Inc()
				next.Reset(min(retry.Delay(attempt), time.Until(expiry)))
			}
		}
	}
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
