package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The metrics listener is a second, separate http.Server. It never shares the public
// mux, so no route table change can expose it, and it answers only GET /metrics to
// peers on the internal observability network.
const (
	defaultMetricsAddr  = ":9090"
	defaultMetricsAllow = "127.0.0.0/8,::1/128,10.202.0.0/24"
	metricsShutdownWait = 5 * time.Second
)

func metricsAddrFromEnv() string {
	if addr := strings.TrimSpace(os.Getenv("METRICS_ADDR")); addr != "" {
		return addr
	}
	return defaultMetricsAddr
}

func metricsAllowFromEnv() string {
	if raw := strings.TrimSpace(os.Getenv("METRICS_ALLOW_CIDRS")); raw != "" {
		return raw
	}
	return defaultMetricsAllow
}

// parseMetricsAllowlist reads a comma-separated CIDR list. An empty or malformed
// list is an error: the listener must never fall back to "everyone".
func parseMetricsAllowlist(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("METRICS_ALLOW_CIDRS entry %q is not a CIDR", part)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("METRICS_ALLOW_CIDRS names no network")
	}
	return prefixes, nil
}

func peerAllowed(remoteAddr string, allow []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range allow {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// newMetricsHandler serves only GET /metrics. Every other path is a 404 and every
// other method on /metrics a 405 (the Go 1.22 mux answers that itself); a peer
// outside the allowlist gets 403 before any metric is gathered.
func newMetricsHandler(gatherer prometheus.Gatherer, allow []netip.Prefix) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
		Timeout:       10 * time.Second,
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !peerAllowed(r.RemoteAddr, allow) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func newMetricsServer(addr string, gatherer prometheus.Gatherer, allow []netip.Prefix) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           newMetricsHandler(gatherer, allow),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
}

// metricsServerStop shuts the metrics listener down; runServer calls it with the
// rest of the staged shutdown. It is a no-op until the listener has started.
var metricsServerStop = func(context.Context) error { return nil }

// startMetricsServer binds first (so a bind failure is reported, not lost in a
// goroutine) and then serves. It returns the bound address for tests.
func startMetricsServer(addr string, gatherer prometheus.Gatherer, allow []netip.Prefix) (string, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("metrics listener: %w", err)
	}
	server := newMetricsServer(addr, gatherer, allow)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics listener stopped: %v", err)
		}
	}()
	metricsServerStop = server.Shutdown
	return listener.Addr().String(), nil
}
