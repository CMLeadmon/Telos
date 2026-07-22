package main

import (
	"context"
	"fmt"
	"testing"
)

// healthReadyFixture wires the global dependencies to the disposable stores and
// marks migrations verified, so the real checkers can run.
func healthReadyFixture(t *testing.T) {
	t.Helper()
	f := withFixture(t)
	oldPool, oldRedis, oldDisp, oldVerified := dbPool, redisClient, outboxDispatcher, migrationsVerified
	dbPool = f.DB
	redisClient = f.Redis
	outboxDispatcher = newOutboxDispatcher(f.DB, f.Redis)
	migrationsVerified = true
	t.Cleanup(func() {
		dbPool, redisClient, outboxDispatcher, migrationsVerified = oldPool, oldRedis, oldDisp, oldVerified
	})
}

func TestHealthReadinessLiveDependencies(t *testing.T) {
	healthReadyFixture(t)
	s := newHealthService(postgresChecker(), redisChecker(), outboxChecker())
	r := s.Readiness(context.Background())
	if r.Status != HealthOK {
		t.Fatalf("readiness with healthy deps = %q: %+v", r.Status, r.Checks)
	}
}

func TestHealthPostgresMigrationMismatch(t *testing.T) {
	healthReadyFixture(t)
	migrationsVerified = false // simulate a drifted schema
	s := newHealthService(postgresChecker())
	r := s.Readiness(context.Background())
	if r.Status != HealthFail {
		t.Fatalf("migration mismatch should fail readiness, got %q", r.Status)
	}
	if r.Checks[0].Detail != "migration_mismatch" {
		t.Fatalf("detail = %q, want migration_mismatch", r.Checks[0].Detail)
	}
}

func TestHealthOutboxBacklogThresholds(t *testing.T) {
	healthReadyFixture(t)
	ctx := context.Background()

	insertPending := func(n int, ageMinutes int) {
		for i := 0; i < n; i++ {
			_, err := dbPool.Exec(ctx, `
				INSERT INTO outbox_events (topic, event_type, aggregate_type, aggregate_id, idempotency_key, created_at)
				VALUES ('t','e','a','1', $1, NOW() - make_interval(mins => $2))`,
				fmt.Sprintf("key-%d-%d", ageMinutes, i), ageMinutes)
			if err != nil {
				t.Fatalf("insert pending: %v", err)
			}
		}
	}

	check := func() HealthCheck {
		return outboxChecker().Check(ctx)
	}

	// Clean: ok.
	if got := check(); got.Status != HealthOK {
		t.Fatalf("empty backlog = %q, want ok", got.Status)
	}
	// A single event older than 2 minutes trips the hard age threshold -> fail.
	insertPending(1, 3)
	if got := check(); got.Status != HealthFail {
		t.Fatalf("stale pending event = %q, want fail (%+v)", got.Status, got)
	}
}
