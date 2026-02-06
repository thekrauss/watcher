package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	EventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "watcher_events_total",
		Help: "Total number of pod events processed by the watcher",
	}, []string{"type", "reason", "namespace"})

	ReconnectsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "watcher_reconnects_total",
		Help: "Total number of times the kubernetes watch has reconnected",
	})

	RestateRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "watcher_restate_requests_total",
		Help: "Total number of requests sent to Restate",
	}, []string{"code"})

	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "watcher_queue_depth",
		Help: "Current depth of the event processing queue",
	})

	EventsDedupedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "watcher_events_deduped_total",
		Help: "Total number of events filtered out by deduplication",
	})
)
