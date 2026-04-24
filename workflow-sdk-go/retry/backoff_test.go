package retry_test

import (
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

func TestDelayProgression(t *testing.T) {
	d1 := retry.Delay(1)
	d2 := retry.Delay(2)
	d3 := retry.Delay(3)

	// Each delay should be larger than the previous (modulo jitter; check with margin)
	// d1 ~ 100ms, d2 ~ 200ms, d3 ~ 400ms (before jitter)
	// With ±20% jitter: d1 in [80ms, 120ms], d2 in [160ms, 240ms], d3 in [320ms, 480ms]
	if d1 < 60*time.Millisecond || d1 > 150*time.Millisecond {
		t.Errorf("d1 out of expected range: %v", d1)
	}
	if d2 < 130*time.Millisecond || d2 > 280*time.Millisecond {
		t.Errorf("d2 out of expected range: %v", d2)
	}
	if d3 < 260*time.Millisecond || d3 > 560*time.Millisecond {
		t.Errorf("d3 out of expected range: %v", d3)
	}
}

func TestDelayNeverExceedsMaxDelay(t *testing.T) {
	for attempt := 1; attempt <= 30; attempt++ {
		d := retry.Delay(attempt)
		if d > retry.MaxDelay {
			t.Errorf("attempt %d: delay %v exceeds MaxDelay %v", attempt, d, retry.MaxDelay)
		}
	}
}

func TestDelayNeverNegative(t *testing.T) {
	for attempt := 1; attempt <= 100; attempt++ {
		d := retry.Delay(attempt)
		if d < 0 {
			t.Errorf("attempt %d: negative delay %v", attempt, d)
		}
	}
}

func TestIsNonRetryable(t *testing.T) {
	err := &retry.NonRetryable{Cause: errTest{}}
	if !retry.IsNonRetryable(err) {
		t.Error("want IsNonRetryable=true for NonRetryable error")
	}
	if retry.IsNonRetryable(errTest{}) {
		t.Error("want IsNonRetryable=false for plain error")
	}
}

type errTest struct{}

func (errTest) Error() string { return "test error" }
