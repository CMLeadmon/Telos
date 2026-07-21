package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestEnqueueOutboxRollbackPublishesNothing(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	tx, _ := f.DB.Begin(ctx)
	EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic: "t", EventType: "e", AggregateType: "a", AggregateID: "1",
		IdempotencyKey: "k1", Payload: json.RawMessage(`{"x":1}`),
	})
	tx.Rollback(ctx)

	var n int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events`).Scan(&n)
	if n != 0 {
		t.Fatalf("rolled-back enqueue left %d rows", n)
	}
}

func TestEnqueueOutboxDuplicateKeyTolerated(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	ev := OutboxEvent{Topic: "t", EventType: "e", AggregateType: "a", AggregateID: "1", IdempotencyKey: "dup"}

	tx, _ := f.DB.Begin(ctx)
	id1, err := EnqueueOutbox(ctx, tx, ev)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := EnqueueOutbox(ctx, tx, ev)
	if err != nil {
		t.Fatalf("duplicate key not tolerated: %v", err)
	}
	tx.Commit(ctx)
	if id1 != id2 {
		t.Fatalf("duplicate key produced two rows: %s vs %s", id1, id2)
	}
	var n int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events`).Scan(&n)
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
}

func TestOutboxDispatcherPublishesAndMarks(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	d := newOutboxDispatcher(f.DB, f.Redis)

	// Subscribe before enqueue.
	sub := f.Redis.Subscribe(ctx, "telos:events:test")
	defer sub.Close()
	ch := sub.Channel()

	tx, _ := f.DB.Begin(ctx)
	EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic: "telos:events:test", EventType: "e", AggregateType: "a", AggregateID: "1",
		IdempotencyKey: "pub1", Payload: json.RawMessage(`{"hello":"world"}`),
	})
	tx.Commit(ctx)

	if err := d.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		if !strings.Contains(msg.Payload, "world") {
			t.Fatalf("published payload = %q", msg.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event was not published to Redis")
	}

	var status string
	f.DB.QueryRow(ctx, `SELECT status FROM outbox_events WHERE idempotency_key='pub1'`).Scan(&status)
	if status != "published" {
		t.Fatalf("event status = %q, want published", status)
	}
}

func TestOutboxRedisOutageRetainsWork(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	// A dispatcher pointed at an unreachable Redis fails to publish and keeps
	// the row pending for retry.
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer broken.Close()
	d := newOutboxDispatcher(f.DB, broken)

	tx, _ := f.DB.Begin(ctx)
	EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic: "telos:events:test", EventType: "e", AggregateType: "a", AggregateID: "1",
		IdempotencyKey: "retry1",
	})
	tx.Commit(ctx)

	d.Drain(ctx)

	var status string
	var attempts int
	f.DB.QueryRow(ctx, `SELECT status, attempts FROM outbox_events WHERE idempotency_key='retry1'`).Scan(&status, &attempts)
	if status != "pending" || attempts == 0 {
		t.Fatalf("after Redis outage status=%q attempts=%d, want pending with a retry", status, attempts)
	}
}

func TestConcurrentDispatchersDoNotDoubleClaim(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Enqueue 30 events.
	tx, _ := f.DB.Begin(ctx)
	for i := 0; i < 30; i++ {
		EnqueueOutbox(ctx, tx, OutboxEvent{
			Topic: "telos:events:test", EventType: "e", AggregateType: "a", AggregateID: itoa(i),
			IdempotencyKey: "cc" + itoa(i),
		})
	}
	tx.Commit(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := newOutboxDispatcher(f.DB, f.Redis)
			for j := 0; j < 5; j++ {
				d.Drain(ctx)
			}
		}()
	}
	wg.Wait()

	// Every event published exactly once (attempts == 1 for all).
	var overPublished int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE status='published' AND attempts <> 1`).Scan(&overPublished)
	if overPublished != 0 {
		t.Fatalf("%d events were claimed/published more than once", overPublished)
	}
	var pending int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE status='pending'`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("%d events left pending", pending)
	}
}

func TestSecurityMutationEnqueuesOutbox(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	oldSink := securityEvents
	securityEvents = OutboxSecurityEventSink{}
	t.Cleanup(func() { securityEvents = oldSink })

	// A role change enqueues exactly one security event.
	var target string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('target','x') RETURNING id`).Scan(&target)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Member')`, target)

	actor := &UserContext{ID: "admin", Roles: []string{"Owner"}, Permissions: []string{"manage_roles"}}
	body := `{"roles":["Moderator"]}`
	r := httptest.NewRequest("PUT", "/api/v1/admin/users/"+target+"/roles", strings.NewReader(body))
	r.SetPathValue("id", target)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, actor))
	rec := httptest.NewRecorder()
	handleAdminSetUserRoles(rec, r)
	if rec.Code != 200 {
		t.Fatalf("role change status %d: %s", rec.Code, rec.Body.String())
	}

	var n int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE event_type='roles_changed' AND aggregate_id=$1`, target).Scan(&n)
	if n != 1 {
		t.Fatalf("role change enqueued %d security events, want 1", n)
	}
}
