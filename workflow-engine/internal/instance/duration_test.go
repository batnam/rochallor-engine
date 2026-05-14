package instance

import (
	"testing"
	"time"
)

func TestParseDuration_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  time.Duration
	}{
		{"PT30S", 30 * time.Second},
		{"PT5M", 5 * time.Minute},
		{"PT2H", 2 * time.Hour},
		{"PT1H30M", 90 * time.Minute},
		{"PT1H30M15S", time.Hour + 30*time.Minute + 15*time.Second},
		{"P1H", time.Hour}, // T prefix is optional
		// Spec (docs/workflow-format.md): "P7D" = 7 days.
		{"P7D", 7 * 24 * time.Hour},
		{"P1D", 24 * time.Hour},
		{"P1DT12H", 24*time.Hour + 12*time.Hour}, // mixed days + hours
		{"P2DT30M", 2*24*time.Hour + 30*time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseDuration(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseDuration_Invalid(t *testing.T) {
	cases := []string{
		"",         // empty
		"30S",      // missing P prefix
		"PT",       // no value/unit
		"PT0S",     // zero is rejected
		"X30S",     // wrong leading char
		"P",        // too short
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := parseDuration(in); err == nil {
				t.Errorf("expected error for %q, got nil", in)
			}
		})
	}
}
