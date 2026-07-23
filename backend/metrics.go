package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	AuthThrottledCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telos_auth_throttled_total",
		Help: "Total count of throttled authentication attempts",
	})
	ActiveSocketCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "telos_active_sockets",
		Help: "Current count of active WebSocket client connections",
	})
	OutboxQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "telos_outbox_queue_size",
		Help: "Current size of the outbox message queue",
	})
)

func init() {
	prometheus.MustRegister(AuthThrottledCount)
	prometheus.MustRegister(ActiveSocketCount)
	prometheus.MustRegister(OutboxQueueSize)
}

func metricsHandler() http.Handler {
	return promhttp.Handler()
}
