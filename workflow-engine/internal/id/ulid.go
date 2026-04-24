// Package id provides ULID generators for the workflow engine's primary keys.
//
// ULIDs are chosen over UUIDv4 for their lexicographic sort order, which
// enables efficient "most-recent rows first" queries without a separate
// timestamp index (per R-008).
//
// The generators are concurrency-safe — each call to New() acquires a global
// mutex over a shared monotonic source so ULIDs generated within the same
// millisecond are still ordered.
package id

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New generates a new, globally-unique ULID string.
// It is safe to call from multiple goroutines concurrently.
func New() string {
	mu.Lock()
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
	mu.Unlock()
	return id.String()
}

// NewInstance is a semantic alias for New used when creating workflow instance IDs.
func NewInstance() string { return New() }

// NewStepExecution is a semantic alias for New used when creating step execution IDs.
func NewStepExecution() string { return New() }

// NewJob is a semantic alias for New used when creating job IDs.
func NewJob() string { return New() }

// NewUserTask is a semantic alias for New used when creating user task IDs.
func NewUserTask() string { return New() }

// NewBoundaryEvent is a semantic alias for New used when creating boundary event schedule IDs.
func NewBoundaryEvent() string { return New() }
