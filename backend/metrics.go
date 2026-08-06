package main

import (
	"fmt"
	"net/http"
	"time"

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
	CatalogReconciledSources = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telos_catalog_reconciled_sources_total",
		Help: "Total catalog sources reconciled by provider and bounded outcome",
	}, []string{"provider", "outcome"})
	CatalogReconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "telos_catalog_reconcile_duration_seconds",
		Help: "Catalog reconciliation duration by provider, operation, and result",
	}, []string{"provider", "operation", "result"})
)

func init() {
	prometheus.MustRegister(AuthThrottledCount)
	prometheus.MustRegister(ActiveSocketCount)
	prometheus.MustRegister(OutboxQueueSize)
	prometheus.MustRegister(CatalogReconciledSources)
	prometheus.MustRegister(CatalogReconcileDuration)
}

type CatalogReconcileOutcome string
type CatalogOperation string
type CatalogResult string

const (
	CatalogOutcomeCreated   CatalogReconcileOutcome = "created"
	CatalogOutcomeRefreshed CatalogReconcileOutcome = "refreshed"
	CatalogOutcomeRestored  CatalogReconcileOutcome = "restored"
	CatalogOutcomeMissing   CatalogReconcileOutcome = "missing"
	CatalogOutcomeAmbiguous CatalogReconcileOutcome = "ambiguous"
	CatalogOutcomeFailed    CatalogReconcileOutcome = "failed"

	CatalogOperationObserve   CatalogOperation = "observe"
	CatalogOperationScan      CatalogOperation = "scan"
	CatalogOperationEnumerate CatalogOperation = "enumerate"

	CatalogResultSuccess CatalogResult = "success"
	CatalogResultFailed  CatalogResult = "failed"
)

var catalogMetricNow = time.Now
var catalogMetricSince = time.Since

func recordCatalogReconciled(provider CatalogProvider, outcome CatalogReconcileOutcome, count float64) error {
	if !validCatalogProvider(provider) {
		return fmt.Errorf("%w: metric provider", errCatalogInvalid)
	}
	switch outcome {
	case CatalogOutcomeCreated, CatalogOutcomeRefreshed, CatalogOutcomeRestored,
		CatalogOutcomeMissing, CatalogOutcomeAmbiguous, CatalogOutcomeFailed:
	default:
		return fmt.Errorf("%w: metric outcome", errCatalogInvalid)
	}
	if count < 0 {
		return fmt.Errorf("%w: metric count", errCatalogInvalid)
	}
	CatalogReconciledSources.WithLabelValues(string(provider), string(outcome)).Add(count)
	return nil
}

func observeCatalogReconcileDuration(provider CatalogProvider, operation CatalogOperation, result CatalogResult, elapsed time.Duration) error {
	if !validCatalogProvider(provider) {
		return fmt.Errorf("%w: metric provider", errCatalogInvalid)
	}
	switch operation {
	case CatalogOperationObserve, CatalogOperationScan, CatalogOperationEnumerate:
	default:
		return fmt.Errorf("%w: metric operation", errCatalogInvalid)
	}
	switch result {
	case CatalogResultSuccess, CatalogResultFailed:
	default:
		return fmt.Errorf("%w: metric result", errCatalogInvalid)
	}
	if elapsed < 0 {
		return fmt.Errorf("%w: metric duration", errCatalogInvalid)
	}
	CatalogReconcileDuration.WithLabelValues(string(provider), string(operation), string(result)).Observe(elapsed.Seconds())
	return nil
}

// RecordCatalogEnumerationFailure records a provider enumeration that failed
// before a complete scan existed. Callers must not call CompleteScan after
// such a failure, so transient upstream errors never make sources unavailable.
func RecordCatalogEnumerationFailure(provider CatalogProvider, elapsed time.Duration) error {
	if elapsed < 0 {
		return fmt.Errorf("%w: metric duration", errCatalogInvalid)
	}
	if err := recordCatalogReconciled(provider, CatalogOutcomeFailed, 1); err != nil {
		return err
	}
	return observeCatalogReconcileDuration(provider, CatalogOperationEnumerate, CatalogResultFailed, elapsed)
}

func metricsHandler() http.Handler {
	return promhttp.Handler()
}
