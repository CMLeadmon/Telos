package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	AuthThrottledCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telos_auth_throttled_total",
		Help: "Total count of throttled authentication attempts",
	})
	// Computed from the live registry on every scrape, so it cannot drift from the
	// real count the way an increment/decrement pair can.
	ActiveSocketCount = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "telos_active_sockets",
		Help: "Current count of active WebSocket client connections",
	}, func() float64 { return float64(sessionRegistryInstance.Total()) })
	// Saturation signals the capacity drill reads: pool and stream use against their configured maxima.
	// Computed on every scrape from the live objects; before they exist (tests, early boot) they read 0.
	DBPoolAcquired = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "telos_db_pool_acquired_connections",
		Help: "Database connections currently checked out of the gateway's pool",
	}, func() float64 {
		if dbPool == nil {
			return 0
		}
		return float64(dbPool.Stat().AcquiredConns())
	})
	DBPoolMax = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "telos_db_pool_max_connections",
		Help: "Configured maximum size of the gateway's database pool",
	}, func() float64 {
		if dbPool == nil {
			return 0
		}
		return float64(dbPool.Stat().MaxConns())
	})
	ActiveStreams = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "telos_active_streams",
		Help: "Long-lived media streams currently admitted",
	}, func() float64 {
		if streamCapacity == nil {
			return 0
		}
		return float64(streamCapacity.InFlight())
	})
	StreamCapacityMax = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "telos_stream_capacity_max",
		Help: "Configured maximum of concurrently admitted media streams",
	}, func() float64 {
		if streamCapacity == nil {
			return 0
		}
		return float64(streamCapacity.globalMax)
	})
	OutboxQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "telos_outbox_queue_size",
		Help: "Current size of the outbox message queue",
	})
	CatalogReconciledSources = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telos_catalog_reconciled_sources_total",
		Help: "Total catalog sources reconciled by provider and bounded outcome",
	}, []string{"provider", "outcome"})
	// Request signals are bounded by construction: the method is one of nine fixed
	// strings and the status is a class, never a raw path, user, channel or item.
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telos_http_requests_total",
		Help: "HTTP requests served by method and status class",
	}, []string{"method", "code_class"})
	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "telos_http_request_duration_seconds",
		Help:    "HTTP request duration by method and status class",
		Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 10, 30},
	}, []string{"method", "code_class"})
	// One series per fixed readiness dependency: 1 when its check is ok.
	DependencyUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "telos_dependency_up",
		Help: "Whether a readiness dependency check is currently ok (1) or not (0)",
	}, []string{"service"})
	CatalogReconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "telos_catalog_reconcile_duration_seconds",
		Help: "Catalog reconciliation duration by provider, operation, and result",
	}, []string{"provider", "operation", "result"})
)

func init() {
	prometheus.MustRegister(AuthThrottledCount)
	prometheus.MustRegister(ActiveSocketCount)
	prometheus.MustRegister(DBPoolAcquired)
	prometheus.MustRegister(DBPoolMax)
	prometheus.MustRegister(ActiveStreams)
	prometheus.MustRegister(StreamCapacityMax)
	prometheus.MustRegister(OutboxQueueSize)
	prometheus.MustRegister(CatalogReconciledSources)
	prometheus.MustRegister(CatalogReconcileDuration)
	prometheus.MustRegister(HTTPRequests)
	prometheus.MustRegister(HTTPRequestDuration)
	prometheus.MustRegister(DependencyUp)
	prometheus.MustRegister(newBackupStatusCollector(backupStatusPathFromEnv))
}

// metricMethods is the complete method vocabulary; anything else is "other" so a
// hostile client cannot mint series.
var metricMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {}, http.MethodPut: {}, http.MethodPatch: {},
	http.MethodDelete: {}, http.MethodOptions: {}, http.MethodConnect: {}, http.MethodTrace: {},
}

func metricMethod(method string) string {
	if _, ok := metricMethods[method]; ok {
		return method
	}
	return "other"
}

func statusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	}
	return "other"
}

// dependencyServices are the only values of the "service" label.
var dependencyServices = map[string]struct{}{
	"postgres": {}, "redis": {}, "outbox": {}, "storage": {}, "jellyfin": {}, "grimmory": {},
}

// recordDependencyStatus publishes one readiness check. Unknown names are dropped
// rather than becoming labels.
func recordDependencyStatus(name string, ok bool) {
	if _, known := dependencyServices[name]; !known {
		return
	}
	value := 0.0
	if ok {
		value = 1
	}
	DependencyUp.WithLabelValues(name).Set(value)
}

// statusWriter records the response status while keeping every optional interface
// a WebSocket upgrade or a streamed response needs.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// metricsMiddleware counts every request that reaches the gateway, including the
// ones admission control rejects.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		method, class := metricMethod(r.Method), statusClass(status)
		HTTPRequests.WithLabelValues(method, class).Inc()
		HTTPRequestDuration.WithLabelValues(method, class).Observe(time.Since(started).Seconds())
	})
}

// ---- backup status ---------------------------------------------------------
//
// The backup command writes a Prometheus textfile atomically under the operations
// state directory, mounted read-only into the gateway. The gateway never accepts
// backup data over the network: it reads exactly two declared series from that file
// and publishes them with fixed names. Anything else in the file, a missing or
// unreadable file, or a file too old to be believable becomes a failure signal, never
// a label.

const (
	backupStatusFile       = "last-backup.prom"
	backupStatusMaxBytes   = 4096
	backupSuccessSeries    = "telos_backup_last_success_timestamp_seconds"
	backupResultSeries     = "telos_backup_last_result"
	backupStatusMaxFuture  = 5 * time.Minute
	backupStatusMaxAgeOver = 366 * 24 * time.Hour
)

func backupStatusPathFromEnv() string {
	dir := strings.TrimSpace(os.Getenv("TELOS_OPS_STATE_PATH"))
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, backupStatusFile)
}

type backupStatus struct {
	lastSuccess float64 // unix seconds; 0 when never
	lastResult  float64 // 1 success, 0 failure
	valid       bool
}

// parseBackupStatus accepts only the two declared series, each at most once, with
// finite plausible values. Comment lines are ignored; anything else is invalid.
func parseBackupStatus(text string, now time.Time) backupStatus {
	var status backupStatus
	seenSuccess, seenResult := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return backupStatus{}
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || value != value || value < 0 {
			return backupStatus{}
		}
		switch fields[0] {
		case backupSuccessSeries:
			if seenSuccess || value > float64(now.Add(backupStatusMaxFuture).Unix()) {
				return backupStatus{}
			}
			if value != 0 && time.Unix(int64(value), 0).Before(now.Add(-backupStatusMaxAgeOver)) {
				return backupStatus{}
			}
			seenSuccess, status.lastSuccess = true, value
		case backupResultSeries:
			if seenResult || (value != 0 && value != 1) {
				return backupStatus{}
			}
			seenResult, status.lastResult = true, value
		default:
			return backupStatus{}
		}
	}
	if !seenSuccess || !seenResult {
		return backupStatus{}
	}
	status.valid = true
	return status
}

type backupStatusCollector struct {
	path func() string
	now  func() time.Time

	successDesc, resultDesc, validDesc *prometheus.Desc
}

func newBackupStatusCollector(path func() string) *backupStatusCollector {
	return &backupStatusCollector{
		path: path, now: time.Now,
		successDesc: prometheus.NewDesc(backupSuccessSeries, "Unix time of the last successful backup, or 0 when unknown", nil, nil),
		resultDesc:  prometheus.NewDesc(backupResultSeries, "Result of the last backup run: 1 success, 0 failure or unknown", nil, nil),
		validDesc:   prometheus.NewDesc("telos_backup_status_valid", "Whether the backup status file was present and well formed (1) or not (0)", nil, nil),
	}
}

func (c *backupStatusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.successDesc
	ch <- c.resultDesc
	ch <- c.validDesc
}

func (c *backupStatusCollector) Collect(ch chan<- prometheus.Metric) {
	var status backupStatus
	if path := c.path(); path != "" {
		if file, err := os.Open(path); err == nil {
			buffer := make([]byte, backupStatusMaxBytes+1)
			n, _ := file.Read(buffer)
			file.Close()
			if n <= backupStatusMaxBytes {
				status = parseBackupStatus(string(buffer[:n]), c.now())
			}
		}
	}
	valid := 0.0
	if status.valid {
		valid = 1
	}
	ch <- prometheus.MustNewConstMetric(c.successDesc, prometheus.GaugeValue, status.lastSuccess)
	ch <- prometheus.MustNewConstMetric(c.resultDesc, prometheus.GaugeValue, status.lastResult)
	ch <- prometheus.MustNewConstMetric(c.validDesc, prometheus.GaugeValue, valid)
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
