package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// All exported metrics are registered against the default registry via promauto
// so they are available immediately after package init, with no explicit
// registration call required by callers (per R-013).
//
// Exceptions: polling-specific metrics are registered conditionally via
// RegisterPollingMetrics (FR-010).

var (
	// ── Definitions ──────────────────────────────────────────────────────────

	// DefinitionsUploadedTotal counts successful definition uploads.
	DefinitionsUploadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "workflow_definitions_uploaded_total",
		Help: "Total number of workflow definitions successfully uploaded.",
	})

	// ── Instances ─────────────────────────────────────────────────────────────

	// InstancesStartedTotal counts new workflow instances created.
	InstancesStartedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "workflow_instances_started_total",
		Help: "Total number of workflow instances started.",
	})

	// InstancesCompletedTotal counts instances that reached a terminal state,
	// labelled by outcome: completed | failed | cancelled.
	InstancesCompletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workflow_instances_completed_total",
		Help: "Total number of workflow instances that reached a terminal state.",
	}, []string{"outcome"})

	// ── Steps ─────────────────────────────────────────────────────────────────

	// StepsExecutedTotal counts step entries, labelled by step_type.
	StepsExecutedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workflow_steps_executed_total",
		Help: "Total number of step executions entered, labelled by step type.",
	}, []string{"step_type"})

	// ── Jobs (Polling mode specific, registered via RegisterPollingMetrics) ───

	// JobPollLatency is a histogram of the time from job creation (enqueue) to
	// successful lock by a worker (poll → lock round-trip), in seconds.
	JobPollLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "job_poll_latency_seconds",
		Help:    "Latency between job creation and the first successful worker lock, in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	// JobLockConflictsTotal counts the number of times a poll attempt found
	// a candidate row already locked by another worker (SKIP LOCKED misses).
	JobLockConflictsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "job_lock_conflicts_total",
		Help: "Total number of job poll attempts that were skipped due to an existing lock.",
	})

	// ── Jobs (Always registered) ──────────────────────────────────────────────

	// JobRetriesTotal counts job retry enqueues.
	JobRetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "job_retries_total",
		Help: "Total number of job retries enqueued after a retryable failure.",
	})

	// ── HTTP ──────────────────────────────────────────────────────────────────

	// HTTPRequestDuration is a histogram of HTTP handler latency in seconds,
	// labelled by method and path pattern.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_requests_duration_seconds",
		Help:    "Duration of HTTP requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	// ── gRPC ──────────────────────────────────────────────────────────────────

	// GRPCRequestDuration is a histogram of gRPC handler latency in seconds,
	// labelled by gRPC method and status code.
	GRPCRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_requests_duration_seconds",
		Help:    "Duration of gRPC requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "code"})

	// ── Engine DB performance (feature 005) ───────────────────────────────────

	// DBTransactionDuration is a histogram of pgx transaction durations in
	// seconds, labelled by tx_type (e.g. "instance.lifecycle",
	// "instance.signal_wait", "instance.complete_user_task"). It measures the
	// wall-clock from BeginTx to Commit/Rollback and surfaces regressions in
	// the narrowed FOR UPDATE window delivered by US1.
	DBTransactionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "engine_db_transaction_duration_seconds",
		Help:    "Duration of engine database transactions in seconds, labelled by tx_type.",
		Buckets: prometheus.DefBuckets,
	}, []string{"tx_type"})

	// DBLockWaitDuration is a histogram of FOR UPDATE / FOR UPDATE SKIP LOCKED
	// acquisition wait time in seconds, labelled by operation (e.g.
	// "instance.for_update", "job.poll_skip_locked"). Pre-acquire latency is
	// the primary signal for lock contention on a hot workflow instance.
	DBLockWaitDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "engine_db_lock_wait_duration_seconds",
		Help:    "Time spent waiting for FOR UPDATE / advisory / row-level locks in seconds, labelled by operation.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})

	// JobTimeoutTotal counts jobs that had their lease expire and were
	// reclaimed by the lease sweeper (FR-007 / SC-005).
	JobTimeoutTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "engine_job_timeout_total",
		Help: "Total number of jobs whose lease expired and were recovered by the lease sweeper.",
	})

	// JobPickupLatency is a histogram of end-to-end job pickup latency in
	// seconds: time.Since(job.created_at) at the successful claim site in
	// Poll. Primary signal for SC-003 verification.
	JobPickupLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "engine_job_pickup_latency_seconds",
		Help: "End-to-end latency from job creation to successful worker claim, in seconds.",
		Buckets: []float64{
			0.001, 0.002, 0.005, 0.010, 0.020, 0.030, 0.050,
			0.075, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0, 10.0,
		},
	})
)

// RegisterPollingMetrics registers metrics that only make sense in polling
// mode. Must be called at startup if DispatchMode is "polling" (FR-010).
func RegisterPollingMetrics(r prometheus.Registerer) {
	r.MustRegister(JobPollLatency)
	r.MustRegister(JobLockConflictsTotal)
	r.MustRegister(JobPickupLatency)
}
