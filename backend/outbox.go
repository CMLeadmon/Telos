package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// OutboxEvent is a durable real-time delivery record enqueued in the same
// transaction as the mutation that produced it.
type OutboxEvent struct {
	Topic          string
	EventType      string
	AggregateType  string
	AggregateID    string
	IdempotencyKey string
	Payload        json.RawMessage
}

// EnqueueOutbox writes an outbox row within tx. A duplicate idempotency key is
// tolerated (returns the existing id) so a retried mutation does not double
// enqueue.
func EnqueueOutbox(ctx context.Context, tx pgx.Tx, event OutboxEvent) (string, error) {
	if event.Topic == "" || event.IdempotencyKey == "" || event.EventType == "" {
		return "", errors.New("outbox event requires topic, event type, and idempotency key")
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO outbox_events (topic, event_type, aggregate_type, aggregate_id, idempotency_key, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING id::text
	`, event.Topic, event.EventType, event.AggregateType, event.AggregateID, event.IdempotencyKey, payload).Scan(&id)
	return id, err
}

// OutboxSecurityEventSink materializes SecurityEventIntents as outbox events
// (replaces the Phase 2 no-op sink).
type OutboxSecurityEventSink struct{}

func (OutboxSecurityEventSink) Record(ctx context.Context, tx pgx.Tx, intent SecurityEventIntent) error {
	payload, _ := json.Marshal(map[string]string{
		"actorId":    intent.ActorID,
		"subjectId":  intent.SubjectID,
		"resourceId": intent.ResourceID,
	})
	// Idempotency key ties the event to the actor/subject/resource/kind so a
	// retried mutation produces exactly one durable event.
	key := "sec:" + intent.Kind + ":" + intent.ActorID + ":" + intent.SubjectID + ":" + intent.ResourceID
	_, err := EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic:          "telos:events:security",
		EventType:      intent.Kind,
		AggregateType:  "user",
		AggregateID:    intent.SubjectID,
		IdempotencyKey: key,
		Payload:        payload,
	})
	return err
}

// OutboxDispatcher claims and publishes pending outbox rows.
type OutboxDispatcher struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

func newOutboxDispatcher(pool *pgxpool.Pool, rdb *redis.Client) *OutboxDispatcher {
	return &OutboxDispatcher{pool: pool, redis: rdb}
}

const (
	outboxBatch      = 100
	outboxMaxBackoff = time.Minute
	outboxRetention  = 7 * 24 * time.Hour
)

// Drain claims one batch of due pending events (FOR UPDATE SKIP LOCKED),
// publishes each through Redis, and marks it published; failures are rescheduled
// with capped exponential backoff. Safe to run concurrently across replicas.
func (d *OutboxDispatcher) Drain(ctx context.Context) error {
	if d.pool == nil {
		return nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id::text, topic, payload, attempts
		FROM outbox_events
		WHERE status = 'pending' AND next_attempt_at <= NOW()
		ORDER BY next_attempt_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, outboxBatch)
	if err != nil {
		return err
	}
	type claimed struct {
		id       string
		topic    string
		payload  []byte
		attempts int
	}
	var batch []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.topic, &c.payload, &c.attempts); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, c)
	}
	rows.Close()

	for _, c := range batch {
		var perr error
		if d.redis != nil {
			perr = d.redis.Publish(ctx, c.topic, string(c.payload)).Err()
		}
		if perr != nil {
			// Reschedule with capped exponential backoff; the row stays pending.
			backoff := time.Duration(1<<minInt(c.attempts, 6)) * time.Second
			if backoff > outboxMaxBackoff {
				backoff = outboxMaxBackoff
			}
			if _, err := tx.Exec(ctx, `
				UPDATE outbox_events SET attempts = attempts + 1, next_attempt_at = NOW() + $2
				WHERE id = $1
			`, c.id, backoff); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE outbox_events SET status = 'published', published_at = NOW(), attempts = attempts + 1
			WHERE id = $1
		`, c.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// cleanup removes published rows older than the retention window.
func (d *OutboxDispatcher) cleanup(ctx context.Context) {
	if d.pool == nil {
		return
	}
	d.pool.Exec(ctx, `DELETE FROM outbox_events WHERE status = 'published' AND published_at < NOW() - $1::interval`,
		outboxRetention.String())
}

// Backlog returns the pending count and the age of the oldest pending event.
func (d *OutboxDispatcher) Backlog(ctx context.Context) (pending int, oldest time.Duration, err error) {
	var oldestAt *time.Time
	err = d.pool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(created_at) FROM outbox_events WHERE status = 'pending'
	`).Scan(&pending, &oldestAt)
	if err != nil {
		return 0, 0, err
	}
	if oldestAt != nil {
		oldest = time.Since(*oldestAt)
	}
	return pending, oldest, nil
}

// Run polls every 250ms and periodically cleans up, until ctx is cancelled.
func (d *OutboxDispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	cleanupTicker := time.NewTicker(time.Hour)
	go func() {
		defer ticker.Stop()
		defer cleanupTicker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := d.Drain(ctx); err != nil && ctx.Err() == nil {
					log.Printf("outbox: drain error: %v", err)
				}
			case <-cleanupTicker.C:
				d.cleanup(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var outboxDispatcher *OutboxDispatcher
