package client

import "fmt"

// HTTPError preserves status information so runners can distinguish a rejected
// callback from a transient transport failure. It still satisfies error.
type HTTPError struct {
	Operation  string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: engine returned %d", e.Operation, e.StatusCode)
}
