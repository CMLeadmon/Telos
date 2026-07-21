//go:build linux

package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func quotaFixture(t *testing.T, perUser int64) (*QuotaGuard, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	// Tiny node reserve so the statfs check on a real temp dir always passes;
	// the per-user limit is what we exercise.
	g := newQuotaGuard(QuotaPolicy{PerUserPhysicalBytes: perUser, NodeReserveBytes: 1, ReservationTTL: 15 * time.Minute}, t.TempDir())
	var uid string
	f.DB.QueryRow(context.Background(), `INSERT INTO users (username, password_hash) VALUES ('quotauser','x') RETURNING id`).Scan(&uid)
	return g, uid
}

func TestQuotaGuardPerUserLimit(t *testing.T) {
	g, uid := quotaFixture(t, 1000)
	ctx := context.Background()

	// Reserve up to the boundary.
	r1, err := g.Reserve(ctx, uid, 600, 600)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	// A second reservation that would exceed 1000 fails at the exact boundary.
	if _, err := g.Reserve(ctx, uid, 500, 500); err == nil {
		t.Fatal("over-quota reservation accepted")
	}
	// After releasing, the same request fits.
	if err := r1.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(ctx, uid, 500, 500); err != nil {
		t.Fatalf("reservation after release rejected: %v", err)
	}
}

func TestQuotaGuardConcurrentNoOverbook(t *testing.T) {
	g, uid := quotaFixture(t, 1000)
	ctx := context.Background()

	const n = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := g.Reserve(ctx, uid, 100, 100); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	// At most 1000/100 = 10 may be granted.
	if granted > 10 {
		t.Fatalf("granted %d reservations, cap is 10 (overbooked)", granted)
	}
	if granted == 0 {
		t.Fatal("no reservations granted")
	}
}

func TestQuotaGuardOverflowRejected(t *testing.T) {
	g, uid := quotaFixture(t, 1<<40)
	ctx := context.Background()
	for _, sz := range []int64{-1, 1 << 62} {
		if _, err := g.Reserve(ctx, uid, sz, 1); err == nil {
			t.Errorf("pathological size %d accepted", sz)
		}
	}
}

func TestQuotaGuardNodeReserve(t *testing.T) {
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	// An enormous node reserve (larger than any real free space) must reject.
	g := newQuotaGuard(QuotaPolicy{PerUserPhysicalBytes: 1 << 40, NodeReserveBytes: 1 << 62, ReservationTTL: time.Minute}, t.TempDir())
	var uid string
	f.DB.QueryRow(context.Background(), `INSERT INTO users (username, password_hash) VALUES ('nodeuser','x') RETURNING id`).Scan(&uid)
	if _, err := g.Reserve(context.Background(), uid, 100, 100); err == nil {
		t.Fatal("node reserve breach accepted")
	}
}
