package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

// statInfo is the minimal FileInfo surface the mount checker uses.
type statInfo interface{ IsDir() bool }

// Indirected filesystem calls so the mount checker is testable without a real
// mount.
var (
	osStat   = func(name string) (statInfo, error) { return os.Stat(name) }
	osCreate = func(name string) (interface{ Close() error }, error) { return os.Create(name) }
	osRemove = os.Remove
)

// ═══════════════════════════════════════════════════════════════════════════
// Health: cheap, coalesced, sanitized liveness and readiness
// ═══════════════════════════════════════════════════════════════════════════

// HealthStatus is a stable public status code. Raw infrastructure errors,
// hostnames, paths, and credentials never appear in a report.
type HealthStatus string

const (
	HealthOK   HealthStatus = "ok"
	HealthWarn HealthStatus = "warn"
	HealthFail HealthStatus = "fail"
)

// HealthCheck is one dependency's sanitized result. Detail is a short stable
// code (never a raw error); Metric is a bounded non-negative number or -1.
type HealthCheck struct {
	Name   string       `json:"name"`
	Status HealthStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Metric int64        `json:"metric,omitempty"`
}

// telosVersion is stamped at build time with
// -ldflags "-X main.telosVersion=<tag>"; "dev" in an unstamped build.
var telosVersion = "dev"

// minClientVersion is the oldest client this gateway will serve. A client below
// it must refuse to connect rather than fail later in ways nobody can diagnose.
const minClientVersion = "0.1.0"

// HealthReport is an immutable snapshot returned to callers.
type HealthReport struct {
	Status HealthStatus  `json:"status"`
	Checks []HealthCheck `json:"checks"`
	// AgeMillis is how stale this snapshot is when served (0 for a fresh probe).
	AgeMillis int64 `json:"ageMillis"`
	// Version and MinClientVersion let a client decide whether it can talk to
	// this node at all. A remote client reads them from the connect screen
	// before it has any credential, so they ride on this unauthenticated
	// response — the tradeoff being that the node's version is public.
	Version          string `json:"version"`
	MinClientVersion string `json:"minClientVersion"`

	generatedAt time.Time
}

// HealthChecker is one cheap dependency probe.
type HealthChecker interface {
	Name() string
	Check(ctx context.Context) HealthCheck
}

const (
	healthCheckTimeout   = 2 * time.Second  // per-dependency child timeout
	healthOverallTimeout = 3 * time.Second  // whole coalesced probe
	healthCacheTTL       = 5 * time.Second  // a report younger than this is fresh
	healthCacheMaxAge    = 15 * time.Second // older than this is unusable
)

// HealthService runs readiness probes with a five-second immutable cache and a
// single coalesced in-flight probe, so any number of concurrent misses invoke
// each dependency exactly once.
type HealthService struct {
	checkers []HealthChecker
	now      func() time.Time

	mu       sync.Mutex
	cached   *HealthReport
	inflight chan struct{}
}

func newHealthService(checkers ...HealthChecker) *HealthService {
	return &HealthService{checkers: checkers, now: time.Now}
}

// Liveness reports process state only: O(1), no network, database, disk, or
// statfs work.
func (s *HealthService) Liveness() HealthReport {
	return HealthReport{
		Status: HealthOK,
		Checks: []HealthCheck{{Name: "process", Status: HealthOK}},
	}
}

// Readiness returns a fresh-or-cached sanitized report. Concurrent misses
// coalesce into one probe detached from any single caller's cancellation but
// bounded by healthOverallTimeout. If the caller's context ends first, the most
// recent still-usable cache is returned; otherwise an unavailable report.
func (s *HealthService) Readiness(ctx context.Context) HealthReport {
	s.mu.Lock()
	if s.cached != nil && s.now().Sub(s.cached.generatedAt) < healthCacheTTL {
		r := s.snapshotLocked()
		s.mu.Unlock()
		return r
	}
	wait := s.inflight
	if wait == nil {
		wait = make(chan struct{})
		s.inflight = wait
		go s.runProbe(wait)
	}
	s.mu.Unlock()

	select {
	case <-wait:
		s.mu.Lock()
		r := s.snapshotLocked()
		s.mu.Unlock()
		return r
	case <-ctx.Done():
		// Return a recent usable cache rather than block on a slow probe.
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cached != nil && s.now().Sub(s.cached.generatedAt) < healthCacheMaxAge {
			return s.snapshotLocked()
		}
		return HealthReport{Status: HealthFail, Checks: []HealthCheck{{Name: "readiness", Status: HealthFail, Detail: "timeout"}}}
	}
}

// runProbe executes all checkers concurrently under a detached, bounded context,
// stores the immutable report, and wakes every waiter.
func (s *HealthService) runProbe(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), healthOverallTimeout)
	defer cancel()

	checks := make([]HealthCheck, len(s.checkers))
	var wg sync.WaitGroup
	for i, c := range s.checkers {
		wg.Add(1)
		go func(i int, c HealthChecker) {
			defer wg.Done()
			cctx, ccancel := context.WithTimeout(ctx, healthCheckTimeout)
			defer ccancel()
			checks[i] = runOneCheck(cctx, c)
		}(i, c)
	}
	wg.Wait()

	report := HealthReport{Status: worstStatus(checks), Checks: checks, generatedAt: s.now()}
	s.mu.Lock()
	s.cached = &report
	s.inflight = nil
	s.mu.Unlock()
	close(done)
}

// runOneCheck isolates a checker so a panic or overrun becomes a fail, never a
// crash or a hang past the child timeout.
func runOneCheck(ctx context.Context, c HealthChecker) (hc HealthCheck) {
	resultCh := make(chan HealthCheck, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultCh <- HealthCheck{Name: c.Name(), Status: HealthFail, Detail: "error"}
			}
		}()
		resultCh <- c.Check(ctx)
	}()
	select {
	case hc = <-resultCh:
		return hc
	case <-ctx.Done():
		return HealthCheck{Name: c.Name(), Status: HealthFail, Detail: "timeout"}
	}
}

func (s *HealthService) snapshotLocked() HealthReport {
	r := *s.cached
	r.Checks = append([]HealthCheck(nil), s.cached.Checks...)
	r.AgeMillis = s.now().Sub(s.cached.generatedAt).Milliseconds()
	if r.AgeMillis < 0 {
		r.AgeMillis = 0
	}
	return r
}

// worstStatus folds a set of checks into an overall status (fail > warn > ok).
func worstStatus(checks []HealthCheck) HealthStatus {
	overall := HealthOK
	for _, c := range checks {
		switch c.Status {
		case HealthFail:
			return HealthFail
		case HealthWarn:
			overall = HealthWarn
		}
	}
	return overall
}

// ═══════════════════════════════════════════════════════════════════════════
// Concrete checkers
// ═══════════════════════════════════════════════════════════════════════════

// funcChecker adapts a name and probe function into a HealthChecker. All
// concrete production and test checkers use this so the service stays generic.
type funcChecker struct {
	name string
	fn   func(ctx context.Context) HealthCheck
}

func (f funcChecker) Name() string                          { return f.name }
func (f funcChecker) Check(ctx context.Context) HealthCheck { return f.fn(ctx) }

func checkerFunc(name string, fn func(ctx context.Context) HealthCheck) HealthChecker {
	return funcChecker{name: name, fn: fn}
}

// migrationsVerified is set true once the serve path confirms the applied
// migration set matches the embedded checksums. A false value fails readiness.
var migrationsVerified bool

// postgresChecker pings the pool and confirms migrations are verified.
func postgresChecker() HealthChecker {
	return checkerFunc("postgres", func(ctx context.Context) HealthCheck {
		if dbPool == nil {
			return HealthCheck{Name: "postgres", Status: HealthFail, Detail: "uninitialized"}
		}
		if err := dbPool.Ping(ctx); err != nil {
			return HealthCheck{Name: "postgres", Status: HealthFail, Detail: "unreachable"}
		}
		if !migrationsVerified {
			return HealthCheck{Name: "postgres", Status: HealthFail, Detail: "migration_mismatch"}
		}
		return HealthCheck{Name: "postgres", Status: HealthOK}
	})
}

// redisChecker pings Redis.
func redisChecker() HealthChecker {
	return checkerFunc("redis", func(ctx context.Context) HealthCheck {
		if redisClient == nil {
			return HealthCheck{Name: "redis", Status: HealthFail, Detail: "uninitialized"}
		}
		if err := redisClient.Ping(ctx).Err(); err != nil {
			return HealthCheck{Name: "redis", Status: HealthFail, Detail: "unreachable"}
		}
		return HealthCheck{Name: "redis", Status: HealthOK}
	})
}

// Outbox backlog thresholds: warn above the soft bound, fail above the hard.
const (
	outboxWarnPending = 100
	outboxFailPending = 1000
	outboxWarnAge     = 30 * time.Second
	outboxFailAge     = 2 * time.Minute
)

// outboxChecker reports pending backlog depth and oldest-pending age against
// bounded thresholds, exposing only the numeric depth.
func outboxChecker() HealthChecker {
	return checkerFunc("outbox", func(ctx context.Context) HealthCheck {
		if outboxDispatcher == nil {
			return HealthCheck{Name: "outbox", Status: HealthFail, Detail: "uninitialized"}
		}
		pending, oldest, err := outboxDispatcher.Backlog(ctx)
		if err != nil {
			return HealthCheck{Name: "outbox", Status: HealthFail, Detail: "query_error"}
		}
		status := HealthOK
		if pending >= outboxWarnPending || oldest >= outboxWarnAge {
			status = HealthWarn
		}
		if pending >= outboxFailPending || oldest >= outboxFailAge {
			status = HealthFail
		}
		return HealthCheck{Name: "outbox", Status: status, Metric: int64(pending)}
	})
}

// upstreamChecker probes a cheap upstream endpoint, mapping any non-2xx or
// transport failure to a sanitized status without leaking the URL.
func upstreamChecker(name, url string, required bool) HealthChecker {
	failStatus := HealthWarn
	if required {
		failStatus = HealthFail
	}
	return checkerFunc(name, func(ctx context.Context) HealthCheck {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return HealthCheck{Name: name, Status: failStatus, Detail: "unreachable"}
		}
		resp, err := upstreamHTTPClient.Do(req)
		if err != nil {
			return HealthCheck{Name: name, Status: failStatus, Detail: "unreachable"}
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return HealthCheck{Name: name, Status: failStatus, Detail: "degraded"}
		}
		return HealthCheck{Name: name, Status: HealthOK}
	})
}

// mountChecker confirms a required storage root exists and is writable using a
// pre-named sentinel file. It never walks or reconciles the tree.
func mountChecker(name, path string) HealthChecker {
	sentinel := path + "/.telos-health-sentinel"
	return checkerFunc(name, func(ctx context.Context) HealthCheck {
		info, err := osStat(path)
		if err != nil || !info.IsDir() {
			return HealthCheck{Name: name, Status: HealthFail, Detail: "missing"}
		}
		f, err := osCreate(sentinel)
		if err != nil {
			return HealthCheck{Name: name, Status: HealthFail, Detail: "read_only"}
		}
		f.Close()
		osRemove(sentinel)
		return HealthCheck{Name: name, Status: HealthOK}
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// HTTP handlers
// ═══════════════════════════════════════════════════════════════════════════

var healthService *HealthService

func writeHealth(w http.ResponseWriter, report HealthReport) {
	// Stamped centrally so every health response carries them, including the
	// uninitialized and degraded paths a connecting client is most likely to hit.
	report.Version = telosVersion
	report.MinClientVersion = minClientVersion
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if report.Status == HealthFail {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(report)
}

func handleLiveness(w http.ResponseWriter, r *http.Request) {
	if healthService == nil {
		writeHealth(w, HealthReport{Status: HealthOK, Checks: []HealthCheck{{Name: "process", Status: HealthOK}}})
		return
	}
	writeHealth(w, healthService.Liveness())
}

func handleReadiness(w http.ResponseWriter, r *http.Request) {
	if healthService == nil {
		writeHealth(w, HealthReport{Status: HealthFail, Checks: []HealthCheck{{Name: "readiness", Status: HealthFail, Detail: "uninitialized"}}})
		return
	}
	writeHealth(w, healthService.Readiness(r.Context()))
}
