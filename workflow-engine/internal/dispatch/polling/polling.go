// Package polling is the no-op Dispatcher / Runtime used when the engine runs
// in its default short-polling mode. The `job` row itself is
// the dispatch signal for poll-based workers; no outbox row is written, no
// broker client is constructed, and no background goroutine is started.
//
// The mechanical significance: a deployment that never sets WE_DISPATCH_MODE
// cannot fail startup because of a missing broker, cannot waste a connection
// on a Kafka admin call, and cannot surface any kafka_outbox-specific metric.
package polling

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Dispatcher is the polling-mode no-op dispatcher.
type Dispatcher struct{}

// Enqueue is a no-op in polling mode. The polling worker claims the job row
// directly via the existing FOR UPDATE SKIP LOCKED poll path.
func (Dispatcher) Enqueue(ctx context.Context, tx pgx.Tx, job dispatch.DispatchJob) error {
	return nil
}

// Runtime is the polling-mode no-op lifecycle.
type Runtime struct {
	d Dispatcher
}

// New returns a polling-mode Runtime.
func New() *Runtime {
	return &Runtime{}
}

// Start is a no-op — polling mode has no broker client or relay goroutine.
func (r *Runtime) Start(ctx context.Context) error { return nil }

// Stop is a no-op.
func (r *Runtime) Stop(ctx context.Context) error { return nil }

// Dispatcher returns the no-op Dispatcher.
func (r *Runtime) Dispatcher() dispatch.Dispatcher { return r.d }
