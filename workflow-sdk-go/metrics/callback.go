package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var CallbackRetries = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "workflow_sdk_callback_retries_total", Help: "Retries of an unchanged handler outcome.",
}, []string{"dispatch", "callback"})

var CallbackPending = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "workflow_sdk_callback_pending", Help: "Outcomes waiting for engine callback confirmation.",
}, []string{"dispatch", "callback"})

var CallbackRejected = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "workflow_sdk_callback_rejected_total", Help: "Permanent callback rejections requiring operator action.",
}, []string{"dispatch", "callback"})

var KafkaRecordErrors = promauto.NewCounter(prometheus.CounterOpts{
	Name: "workflow_sdk_kafka_record_errors_total", Help: "Undecodable dispatch records; offsets remain uncommitted.",
})

var callbackMu sync.Mutex
var callbackStarts = map[[2]string]map[*time.Time]struct{}{}

func init() {
	for _, mode := range []string{"polling", "kafka"} {
		for _, kind := range []string{"complete", "fail"} {
			key := [2]string{mode, kind}
			callbackStarts[key] = map[*time.Time]struct{}{}
			promauto.NewGaugeFunc(prometheus.GaugeOpts{
				Name:        "workflow_sdk_callback_oldest_pending_seconds",
				Help:        "Age of the oldest unconfirmed callback; zero when none are pending.",
				ConstLabels: prometheus.Labels{"dispatch": mode, "callback": kind},
			}, func() float64 {
				callbackMu.Lock()
				defer callbackMu.Unlock()
				var age float64
				for start := range callbackStarts[key] {
					if elapsed := time.Since(*start).Seconds(); elapsed > age {
						age = elapsed
					}
				}
				return age
			})
		}
	}
}

// TrackCallback uses only the two dispatch modes and two callback kinds as
// labels. Job, partition and offset identities belong in logs, not metrics.
func TrackCallback(mode, kind string) func() {
	key := [2]string{mode, kind}
	start := time.Now()
	callbackMu.Lock()
	callbackStarts[key][&start] = struct{}{}
	callbackMu.Unlock()
	CallbackPending.WithLabelValues(mode, kind).Inc()
	return func() {
		callbackMu.Lock()
		delete(callbackStarts[key], &start)
		callbackMu.Unlock()
		CallbackPending.WithLabelValues(mode, kind).Dec()
	}
}
