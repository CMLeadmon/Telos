package main

import (
	"net/http"
	"os"
	"strconv"
	"sync"
)

// StreamCapacity is the per-node admission control for concurrent long binary
// streams (audio, video segments, HLS media). It bounds both the global number
// of in-flight streams and the number held by any single user, so one user
// cannot starve the node and the node cannot oversubscribe upstream capacity.
type StreamCapacity struct {
	mu        sync.Mutex
	globalMax int
	perUser   int
	global    int
	byUser    map[string]int
}

func newStreamCapacity(globalMax, perUserMax int) *StreamCapacity {
	if globalMax <= 0 {
		globalMax = 100
	}
	if perUserMax <= 0 {
		perUserMax = 6
	}
	return &StreamCapacity{globalMax: globalMax, perUser: perUserMax, byUser: map[string]int{}}
}

// Acquire admits one stream for userID, returning a single-use release and
// ok=true, or ok=false when either the global or per-user bound is reached.
// Admission is decided before any upstream work is opened.
func (c *StreamCapacity) Acquire(userID string) (release func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.global >= c.globalMax || c.byUser[userID] >= c.perUser {
		return nil, false
	}
	c.global++
	c.byUser[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.global--
			if c.byUser[userID] > 0 {
				c.byUser[userID]--
			}
			if c.byUser[userID] == 0 {
				delete(c.byUser, userID)
			}
		})
	}, true
}

// InFlight reports the current global count (for tests and health reporting).
func (c *StreamCapacity) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.global
}

// streamCapacity is the process-wide admission controller; nil disables gating.
var streamCapacity *StreamCapacity

// acquireStreamSlot admits a long stream for the request's user, writing a 503
// and returning ok=false when capacity is exhausted. When gating is disabled it
// admits with a no-op release.
func acquireStreamSlot(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	if streamCapacity == nil {
		return func() {}, true
	}
	userID := ""
	if u, _ := r.Context().Value(userContextKey).(*UserContext); u != nil {
		userID = u.ID
	}
	rel, ok := streamCapacity.Acquire(userID)
	if !ok {
		w.Header().Set("Retry-After", "5")
		writeAPIError(w, r, http.StatusServiceUnavailable, "capacity", "The service is at streaming capacity; please retry shortly.")
		return nil, false
	}
	return rel, true
}

// envInt reads a non-negative integer environment override, or def when unset
// or invalid.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}
