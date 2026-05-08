package instance

import (
	"fmt"
	"time"
)

// parseDuration parses an ISO-8601 duration string (P7D, PT30S, PT5M, PT2H,
// P1DT12H) into a time.Duration. Supports D (days, treated as 24×Hour), H,
// M, S; the leading 'P' and the optional date/time separator 'T' are
// skipped. Year/month components are not supported (no fixed conversion).
func parseDuration(iso string) (time.Duration, error) {
	if len(iso) < 3 || iso[0] != 'P' {
		return 0, fmt.Errorf("invalid ISO-8601 duration: %q", iso)
	}
	rest := iso[1:]
	var total time.Duration
	i := 0
	for i < len(rest) {
		// 'T' is the date/time separator — skip it and continue parsing.
		if rest[i] == 'T' {
			i++
			continue
		}
		j := i
		for j < len(rest) && (rest[j] >= '0' && rest[j] <= '9' || rest[j] == '.') {
			j++
		}
		if j >= len(rest) || j == i {
			// No unit char after the digits, or no digits before this char.
			break
		}
		unit := rest[j]
		numStr := rest[i:j]
		var n float64
		fmt.Sscanf(numStr, "%f", &n)
		switch unit {
		case 'D':
			total += time.Duration(n * 24 * float64(time.Hour))
		case 'H':
			total += time.Duration(n * float64(time.Hour))
		case 'M':
			total += time.Duration(n * float64(time.Minute))
		case 'S':
			total += time.Duration(n * float64(time.Second))
		}
		i = j + 1
	}
	if total == 0 {
		return 0, fmt.Errorf("invalid or zero ISO-8601 duration: %q", iso)
	}
	return total, nil
}
