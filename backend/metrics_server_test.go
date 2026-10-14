package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var loopbackOnly = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

func TestMetricsServerServesOnlyMetrics(t *testing.T) {
	address, err := startMetricsServer("127.0.0.1:0", prometheus.DefaultGatherer, loopbackOnly)
	if err != nil {
		t.Fatal(err)
	}
	get := func(method, path string) (int, string) {
		request, _ := http.NewRequest(method, "http://"+address+path, nil)
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if code, body := get(http.MethodGet, "/metrics"); code != 200 || !strings.Contains(body, "telos_http_requests_total") && !strings.Contains(body, "telos_active_sockets") {
		t.Fatalf("GET /metrics = %d, body lacks Telos series", code)
	}
	for _, path := range []string{"/", "/api/v1/health", "/api/v1/health/ready", "/metrics/", "/metrics/extra", "/debug/pprof/", "/admin", "//metrics"} {
		// The mux cleans "//metrics" into a redirect to /metrics itself; nothing else may answer.
		if code, _ := get(http.MethodGet, path); code != http.StatusNotFound && !(path == "//metrics" && code >= 300 && code < 400) {
			t.Errorf("GET %s = %d, want 404 (the listener serves only /metrics)", path, code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if code, _ := get(method, "/metrics"); code != http.StatusMethodNotAllowed {
			t.Errorf("%s /metrics = %d, want 405", method, code)
		}
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), metricsShutdownWait)
	defer cancel()
	if err := metricsServerStop(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("an idle metrics listener took %v to stop", elapsed)
	}
	if _, err := net.DialTimeout("tcp", address, 300*time.Millisecond); err == nil {
		t.Error("the metrics port still accepts connections after shutdown")
	}
	metricsServerStop = func(context.Context) error { return nil }
}

func TestMetricsServerRefusesPeersOutsideTheObservabilityNetwork(t *testing.T) {
	allow, err := parseMetricsAllowlist("10.202.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	handler := newMetricsHandler(prometheus.NewRegistry(), allow)
	for remote, want := range map[string]int{
		"10.202.0.9:5555":          http.StatusOK,
		"[::ffff:10.202.0.9]:5555": http.StatusOK,
		"10.201.0.2:5555":          http.StatusForbidden, // the ingress network (Traefik)
		"10.89.0.4:5555":           http.StatusForbidden, // another internal network
		"203.0.113.9:5555":         http.StatusForbidden,
		"not-an-address":           http.StatusForbidden,
		"":                         http.StatusForbidden,
	} {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		request.RemoteAddr = remote
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Errorf("peer %q got %d, want %d", remote, recorder.Code, want)
		}
	}
}

func TestMetricsAllowlistNeverFallsBackToEveryone(t *testing.T) {
	for _, bad := range []string{"", " , ", "banana", "10.0.0.0/33", "10.0.0.1", "0.0.0.0"} {
		if _, err := parseMetricsAllowlist(bad); err == nil {
			t.Errorf("parseMetricsAllowlist(%q) succeeded; an unusable list must be an error", bad)
		}
	}
	prefixes, err := parseMetricsAllowlist(defaultMetricsAllow)
	if err != nil || len(prefixes) != 3 {
		t.Fatalf("default allowlist = %v, %v", prefixes, err)
	}
}

// The metrics port is never reachable through the public mux, whatever the route
// table does: it is a different server.
func TestMetricsAreNotInThePublicGateway(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	handler := gatewayHandler(mux, securityConfig)
	for _, path := range []string{"/metrics", "/api/v1/metrics", "/api/metrics", "/debug/vars"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if body := recorder.Body.String(); strings.Contains(body, "telos_") || strings.Contains(body, "# HELP") {
			t.Errorf("GET %s on the public gateway returned metrics", path)
		}
	}
}

// Label names are a closed vocabulary and request labels carry no raw path, user,
// channel, item, query, host or token.
func TestMetricsPrivacy(t *testing.T) {
	secrets := []string{"alice", "hunter2", "channel-7f3a", "item-9c1d", "q=find", "evil.example", "eyJhbGciOi"}
	handler := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "boom") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	for _, target := range []string{
		"/api/v1/users/alice/avatar", "/api/v1/channels/channel-7f3a/messages?q=find", "/api/v1/media/items/item-9c1d",
		"/api/v1/boom?token=eyJhbGciOi", "/x/y/z",
	} {
		request := httptest.NewRequest("GET", target, nil)
		request.Host = "evil.example"
		request.Header.Set("Authorization", "Bearer eyJhbGciOi")
		request.SetBasicAuth("alice", "hunter2")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	odd := httptest.NewRequest("GET", "/z", nil)
	odd.Method = "ALICE-VERB"
	handler.ServeHTTP(httptest.NewRecorder(), odd)
	recordDependencyStatus("alice", true) // not a dependency
	recordDependencyStatus("postgres", true)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	allowedLabels := map[string]bool{"method": true, "code_class": true, "service": true, "provider": true, "outcome": true,
		"operation": true, "result": true, "le": true, "quantile": true}
	classes := map[string]bool{"1xx": true, "2xx": true, "3xx": true, "4xx": true, "5xx": true, "other": true}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "telos_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !allowedLabels[label.GetName()] {
					t.Errorf("%s carries label %q outside the closed vocabulary", family.GetName(), label.GetName())
				}
				for _, secret := range secrets {
					if strings.Contains(strings.ToLower(label.GetValue()), strings.ToLower(secret)) {
						t.Errorf("%s label %s=%q leaks %q", family.GetName(), label.GetName(), label.GetValue(), secret)
					}
				}
				switch label.GetName() {
				case "code_class":
					if !classes[label.GetValue()] {
						t.Errorf("%s code_class %q is not a status class", family.GetName(), label.GetValue())
					}
				case "method":
					if _, ok := metricMethods[label.GetValue()]; !ok && label.GetValue() != "other" {
						t.Errorf("%s method %q is outside the fixed set", family.GetName(), label.GetValue())
					}
				case "service":
					if _, ok := dependencyServices[label.GetValue()]; !ok {
						t.Errorf("%s service %q is not a fixed dependency", family.GetName(), label.GetValue())
					}
				}
			}
		}
	}
}

func gaugeSeries(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("series %s is not registered", name)
	return nil
}

func TestDependencyGaugeFollowsReadiness(t *testing.T) {
	service := newHealthService(
		checkerFunc("postgres", func(context.Context) HealthCheck { return HealthCheck{Name: "postgres", Status: HealthOK} }),
		checkerFunc("jellyfin", func(context.Context) HealthCheck {
			return HealthCheck{Name: "jellyfin", Status: HealthFail, Detail: "degraded"}
		}),
	)
	service.Readiness(context.Background())
	values := map[string]float64{}
	for _, metric := range gaugeSeries(t, "telos_dependency_up").Metric {
		values[metric.Label[0].GetValue()] = metric.Gauge.GetValue()
	}
	if values["postgres"] != 1 || values["jellyfin"] != 0 {
		t.Fatalf("dependency gauge = %v, want postgres 1 and jellyfin 0", values)
	}
}

func TestActiveSocketsFollowTheRegistry(t *testing.T) {
	registry := newSessionRegistry()
	if registry.Total() != 0 {
		t.Fatal("a fresh registry is not empty")
	}
	release1, ok1 := registry.Register("s1", "u1", &fakeConn{})
	release2, ok2 := registry.Register("s2", "u1", &fakeConn{})
	release3, ok3 := registry.Register("s3", "u2", &fakeConn{})
	if !ok1 || !ok2 || !ok3 || registry.Total() != 3 {
		t.Fatalf("total after three sockets = %d", registry.Total())
	}
	release2()
	registry.RevokeUser("u2")
	release1()
	release3() // already revoked: must not drive the count negative
	if registry.Total() != 0 {
		t.Fatalf("total after every socket is gone = %d", registry.Total())
	}
	if len(gaugeSeries(t, "telos_active_sockets").Metric) != 1 {
		t.Fatal("telos_active_sockets must be exported as one series")
	}
}

func TestSaturationGaugesAreExportedAndTrackTheLimiter(t *testing.T) {
	previous := streamCapacity
	defer func() { streamCapacity = previous }()
	streamCapacity = newStreamCapacity(7, 2)
	release, ok := streamCapacity.Acquire("u1")
	if !ok {
		t.Fatal("a stream should be admitted")
	}
	value := func(name string) float64 {
		series := gaugeSeries(t, name)
		if len(series.Metric) != 1 {
			t.Fatalf("%s must be exported as one series, got %d", name, len(series.Metric))
		}
		return series.Metric[0].GetGauge().GetValue()
	}
	if value("telos_active_streams") != 1 || value("telos_stream_capacity_max") != 7 {
		t.Fatalf("stream gauges = %v/%v, want 1/7", value("telos_active_streams"), value("telos_stream_capacity_max"))
	}
	release()
	if value("telos_active_streams") != 0 {
		t.Fatalf("active streams after release = %v", value("telos_active_streams"))
	}
	// With no pool (unit tests, early boot) the pool gauges read zero rather than panic.
	if value("telos_db_pool_acquired_connections") != 0 || value("telos_db_pool_max_connections") != 0 {
		t.Fatal("pool gauges must read 0 without a pool")
	}
}

func TestBackupStatusIsBoundedAndNeverCarriesLabels(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-2 * time.Hour).Unix()
	cases := []struct {
		name  string
		text  string
		valid bool
	}{
		{"well formed", "# HELP x y\ntelos_backup_last_success_timestamp_seconds 1791975600\ntelos_backup_last_result 1\n", true},
		{"never succeeded", "telos_backup_last_success_timestamp_seconds 0\ntelos_backup_last_result 0\n", true},
		{"a label sneaks in", "telos_backup_last_success_timestamp_seconds{path=\"/x\"} 1791975600\ntelos_backup_last_result 1\n", false},
		{"an extra series", "telos_backup_last_success_timestamp_seconds 1791975600\ntelos_backup_last_result 1\nup 1\n", false},
		{"a duplicate", "telos_backup_last_result 1\ntelos_backup_last_result 1\ntelos_backup_last_success_timestamp_seconds 1791975600\n", false},
		{"missing the result", "telos_backup_last_success_timestamp_seconds 1791975600\n", false},
		{"result is not 0 or 1", "telos_backup_last_success_timestamp_seconds 1791975600\ntelos_backup_last_result 7\n", false},
		{"not a number", "telos_backup_last_success_timestamp_seconds soon\ntelos_backup_last_result 1\n", false},
		{"negative", "telos_backup_last_success_timestamp_seconds -5\ntelos_backup_last_result 1\n", false},
		{"in the far future", "telos_backup_last_success_timestamp_seconds 9999999999\ntelos_backup_last_result 1\n", false},
		{"absurdly old", "telos_backup_last_success_timestamp_seconds 1000\ntelos_backup_last_result 1\n", false},
		{"an extra field", "telos_backup_last_success_timestamp_seconds 1791975600 17919756000\ntelos_backup_last_result 1\n", false},
	}
	_ = recent
	for _, tc := range cases {
		if got := parseBackupStatus(tc.text, now).valid; got != tc.valid {
			t.Errorf("%s: valid = %v, want %v", tc.name, got, tc.valid)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "last-backup.prom")
	collector := newBackupStatusCollector(func() string { return path })
	collector.now = func() time.Time { return now }
	read := func() map[string]float64 {
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector)
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, family := range families {
			if len(family.Metric[0].Label) != 0 {
				t.Errorf("%s has labels", family.GetName())
			}
			out[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
		return out
	}
	if got := read(); got["telos_backup_status_valid"] != 0 || got["telos_backup_last_result"] != 0 {
		t.Errorf("a missing file must read as invalid and failed: %v", got)
	}
	if err := os.WriteFile(path, []byte("telos_backup_last_success_timestamp_seconds 1791975600\ntelos_backup_last_result 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := read(); got["telos_backup_status_valid"] != 1 || got["telos_backup_last_result"] != 1 || got["telos_backup_last_success_timestamp_seconds"] != 1791975600 {
		t.Errorf("a good file was not published: %v", got)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", backupStatusMaxBytes+10)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := read(); got["telos_backup_status_valid"] != 0 {
		t.Errorf("an oversized file must be invalid: %v", got)
	}
	empty := newBackupStatusCollector(func() string { return "" })
	registry := prometheus.NewRegistry()
	registry.MustRegister(empty)
	if families, err := registry.Gather(); err != nil || len(families) != 3 {
		t.Errorf("an unconfigured path must still export three series: %v %v", len(families), err)
	}
}
