package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingChecker records how many times it is invoked and can be made slow so
// concurrent callers coalesce onto one probe.
type countingChecker struct {
	name   string
	calls  atomic.Int64
	delay  time.Duration
	status HealthStatus
	block  chan struct{} // if non-nil, Check waits for it
}

func (c *countingChecker) Name() string { return c.name }
func (c *countingChecker) Check(ctx context.Context) HealthCheck {
	c.calls.Add(1)
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return HealthCheck{Name: c.name, Status: HealthFail, Detail: "timeout"}
		}
	}
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return HealthCheck{Name: c.name, Status: c.status}
}

func TestLivenessIsCheap(t *testing.T) {
	c := &countingChecker{name: "dep", status: HealthOK}
	s := newHealthService(c)
	for i := 0; i < 100; i++ {
		r := s.Liveness()
		if r.Status != HealthOK {
			t.Fatalf("liveness status = %q", r.Status)
		}
	}
	if c.calls.Load() != 0 {
		t.Fatalf("liveness invoked a dependency %d times, want 0", c.calls.Load())
	}
}

func TestHealthCoalescing(t *testing.T) {
	c := &countingChecker{name: "dep", status: HealthOK, delay: 40 * time.Millisecond}
	s := newHealthService(c)

	const n = 500
	var wg sync.WaitGroup
	statuses := make([]HealthStatus, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = s.Readiness(context.Background()).Status
		}(i)
	}
	wg.Wait()

	if c.calls.Load() != 1 {
		t.Fatalf("500 concurrent misses invoked the dependency %d times, want 1", c.calls.Load())
	}
	for _, st := range statuses {
		if st != HealthOK {
			t.Fatalf("a coalesced caller saw %q, want ok", st)
		}
	}
}

func TestHealthCacheAndExpiry(t *testing.T) {
	c := &countingChecker{name: "dep", status: HealthOK}
	s := newHealthService(c)
	now := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	s.Readiness(context.Background())
	s.Readiness(context.Background())
	if c.calls.Load() != 1 {
		t.Fatalf("within cache TTL invoked %d times, want 1", c.calls.Load())
	}
	// Past the 5s TTL, a new probe runs.
	advance(6 * time.Second)
	s.Readiness(context.Background())
	if c.calls.Load() != 2 {
		t.Fatalf("after TTL invoked %d times, want 2", c.calls.Load())
	}
}

func TestHealthWorstStatus(t *testing.T) {
	cases := []struct {
		checks []HealthCheck
		want   HealthStatus
	}{
		{[]HealthCheck{{Status: HealthOK}, {Status: HealthOK}}, HealthOK},
		{[]HealthCheck{{Status: HealthOK}, {Status: HealthWarn}}, HealthWarn},
		{[]HealthCheck{{Status: HealthWarn}, {Status: HealthFail}}, HealthFail},
	}
	for _, c := range cases {
		if got := worstStatus(c.checks); got != c.want {
			t.Errorf("worstStatus(%v) = %q, want %q", c.checks, got, c.want)
		}
	}
}

func TestHealthCallerCancellationNoCache(t *testing.T) {
	block := make(chan struct{})
	c := &countingChecker{name: "dep", status: HealthOK, block: block}
	s := newHealthService(c)
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: no usable cache exists yet
	r := s.Readiness(ctx)
	if r.Status != HealthFail {
		t.Fatalf("cancelled caller with no cache got %q, want fail", r.Status)
	}
}

func TestHealthCallerCancellationServesStaleCache(t *testing.T) {
	c := &countingChecker{name: "dep", status: HealthOK}
	s := newHealthService(c)
	now := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }

	// Prime a cache.
	if r := s.Readiness(context.Background()); r.Status != HealthOK {
		t.Fatalf("prime status %q", r.Status)
	}
	// Age it beyond TTL but within the 15s usable window, then cancel the caller.
	mu.Lock()
	now = now.Add(8 * time.Second)
	mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := s.Readiness(ctx)
	if r.Status != HealthOK {
		t.Fatalf("expected usable stale cache, got %q", r.Status)
	}
}

func TestPanickingCheckerBecomesFail(t *testing.T) {
	s := newHealthService(checkerFunc("boom", func(ctx context.Context) HealthCheck {
		panic("upstream detail that must never surface")
	}))
	r := s.Readiness(context.Background())
	if r.Status != HealthFail {
		t.Fatalf("panicking checker status = %q, want fail", r.Status)
	}
	if len(r.Checks) != 1 || r.Checks[0].Detail == "upstream detail that must never surface" {
		t.Fatalf("panic detail leaked: %+v", r.Checks)
	}
}

func TestMountChecker(t *testing.T) {
	oldStat, oldCreate, oldRemove := osStat, osCreate, osRemove
	defer func() { osStat, osCreate, osRemove = oldStat, oldCreate, oldRemove }()

	// Missing directory -> fail.
	osStat = func(string) (statInfo, error) { return nil, errors.New("nope") }
	if got := mountCheckWith(t, false, false); got.Status != HealthFail || got.Detail != "missing" {
		t.Fatalf("missing mount: %+v", got)
	}
	// Present but read-only -> fail.
	if got := mountCheckWith(t, true, true); got.Status != HealthFail || got.Detail != "read_only" {
		t.Fatalf("read-only mount: %+v", got)
	}
	// Present and writable -> ok.
	if got := mountCheckWith(t, true, false); got.Status != HealthOK {
		t.Fatalf("writable mount: %+v", got)
	}
}

// fakeInfo satisfies the os.FileInfo subset the mount checker uses.
type fakeInfo struct{ dir bool }

func (f fakeInfo) IsDir() bool { return f.dir }

func mountCheckWith(t *testing.T, present, readOnly bool) HealthCheck {
	t.Helper()
	osStat = func(string) (statInfo, error) {
		if !present {
			return nil, errors.New("missing")
		}
		return fakeInfo{dir: true}, nil
	}
	osCreate = func(string) (interface{ Close() error }, error) {
		if readOnly {
			return nil, errors.New("read-only")
		}
		return nopCloser{}, nil
	}
	osRemove = func(string) error { return nil }
	return mountChecker("storage", "/data/shared").Check(context.Background())
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// A remote client reads the node's version from the connect screen before it
// holds any credential, so these have to be present on the unauthenticated
// health response — and on every branch of it, including the ones a client
// hitting a half-started node will actually see.
func TestHealthResponseAdvertisesVersionOnEveryBranch(t *testing.T) {
	for _, report := range []HealthReport{
		{Status: HealthOK},
		{Status: HealthWarn, Checks: []HealthCheck{{Name: "redis", Status: HealthWarn}}},
		{Status: HealthFail, Checks: []HealthCheck{{Name: "readiness", Status: HealthFail, Detail: "uninitialized"}}},
	} {
		rec := httptest.NewRecorder()
		writeHealth(rec, report)

		var body struct {
			Version          string `json:"version"`
			MinClientVersion string `json:"minClientVersion"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode health body: %v", err)
		}
		if body.Version != telosVersion || body.Version == "" {
			t.Fatalf("status %q: version = %q, want %q", report.Status, body.Version, telosVersion)
		}
		if body.MinClientVersion != minClientVersion || body.MinClientVersion == "" {
			t.Fatalf("status %q: minClientVersion = %q, want %q", report.Status, body.MinClientVersion, minClientVersion)
		}
	}
}
