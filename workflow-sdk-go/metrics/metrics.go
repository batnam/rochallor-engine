// Package metrics registers Prometheus metrics for the workflow Go SDK.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PollLatency is a histogram of the time from poll call to first job lock.
	PollLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "workflow_sdk_poll_latency_seconds",
		Help:    "Latency of PollJobs calls in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	// LockConflicts counts poll rounds that returned zero jobs (empty queue or contention).
	LockConflicts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "workflow_sdk_lock_conflicts_total",
		Help: "Number of poll rounds that returned zero jobs.",
	})

	// HandlerLatency is a histogram of handler execution time, labelled by jobType.
	HandlerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "workflow_sdk_handler_latency_seconds",
		Help:    "Duration of handler executions in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"job_type"})

	// RetriesTotal counts retry enqueues from the SDK side.
	RetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workflow_sdk_retries_total",
		Help: "Number of jobs retried by the SDK runner.",
	}, []string{"job_type"})

	// JobsCompletedTotal counts completed jobs, labelled by outcome.
	JobsCompletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workflow_sdk_jobs_completed_total",
		Help: "Number of jobs completed, labelled by outcome: success or failure.",
	}, []string{"job_type", "outcome"})
)
