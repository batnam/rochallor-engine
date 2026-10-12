package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var Heartbeats = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "workflow_sdk_heartbeat_total", Help: "Heartbeat results: accepted, retry, lease_lost, rejected.",
}, []string{"result"})
