// Package retry implements the R-009 backoff policy.
//
// Policy: base 100 ms, factor 2.0, jitter ±20%, max delay 30 s.
// The attempt budget comes from the job's retryCount field.
// Returning NonRetryable from a handler bypasses all retry logic.
package retry

import (
	"errors"
	"math/rand"
	"time"
)

const (
	BaseDelay  = 100 * time.Millisecond
	Factor     = 2.0
	MaxDelay   = 30 * time.Second
	JitterFrac = 0.20 // ±20%
)

// NonRetryable wraps an error and signals the runner to call FailJob with
// retryable=false, skipping the retry budget entirely.
type NonRetryable struct {
	Cause error
}

func (e *NonRetryable) Error() string { return "non-retryable: " + e.Cause.Error() }
func (e *NonRetryable) Unwrap() error { return e.Cause }

// IsNonRetryable returns true if err (or any wrapped cause) is a NonRetryable.
func IsNonRetryable(err error) bool {
	var nr *NonRetryable
	return errors.As(err, &nr)
}

// Delay returns the back-off duration for the given attempt number (1-based).
// The returned duration already includes jitter.
func Delay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	d := float64(BaseDelay)
	for i := 1; i < attempt; i++ {
		d *= Factor
		if d > float64(MaxDelay) {
			d = float64(MaxDelay)
			break
		}
	}
	// Apply ±JitterFrac jitter
	jitter := d * JitterFrac * (2*rand.Float64() - 1)
	total := time.Duration(d + jitter)
	if total < 0 {
		total = 0
	}
	if total > MaxDelay {
		total = MaxDelay
	}
	return total
}
