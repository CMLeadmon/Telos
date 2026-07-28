package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func eventFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	mk := func(n string) string {
		var id string
		f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash) VALUES ($1,'x') RETURNING id::text`, n).Scan(&id)
		return id
	}
	return f.DB, mk("recip"), mk("other")
}

func appendEvents(t *testing.T, db *pgxpool.Pool, recipientID string, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	var seqs []int64
	for i := 0; i < n; i++ {
		s, err := insertUserEvent(ctx, tx, recipientID, "mention", "message", "m", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		seqs = append(seqs, s)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return seqs
}

func TestRecordUserEventIsIdempotent(t *testing.T) {
	db, recip, actor := eventFixture(t)
	ctx := context.Background()

	in := UserEventInput{
		RecipientID:    recip,
		ActorID:        actor,
		Kind:           "mention",
		ResourceType:   "message",
		ResourceID:     "msg-1",
		IdempotencyKey: "test:dedupe:1",
		Payload:        json.RawMessage(`{"channelId":"c1"}`),
	}

	record := func() int64 {
		t.Helper()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		seq, err := RecordUserEvent(ctx, tx, in)
		if err != nil {
			t.Fatalf("RecordUserEvent: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return seq
	}

	first := record()
	second := record() // replay with the same idempotency key

	if first != second {
		t.Fatalf("replay appended a new event: first=%d second=%d", first, second)
	}

	var events, outbox int
	db.QueryRow(ctx, `SELECT count(*) FROM user_events WHERE idempotency_key=$1`, in.IdempotencyKey).Scan(&events)
	db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE topic=$1`, "telos:user:"+recip).Scan(&outbox)
	if events != 1 || outbox != 1 {
		t.Fatalf("idempotency leaked: events=%d outbox=%d, want 1/1", events, outbox)
	}

	// The actor is folded into the payload since user_events has no actor column.
	var payload []byte
	db.QueryRow(ctx, `SELECT payload FROM user_events WHERE idempotency_key=$1`, in.IdempotencyKey).Scan(&payload)
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got["actorId"] != actor {
		t.Fatalf("actorId not preserved in payload: %v", got["actorId"])
	}
	if got["channelId"] != "c1" {
		t.Fatalf("caller payload lost: %v", got["channelId"])
	}
}

func TestUserEventCatchUpMonotonicAndHighWater(t *testing.T) {
	db, recip, _ := eventFixture(t)
	ctx := context.Background()
	seqs := appendEvents(t, db, recip, 5)

	// throughSequence=0 samples the current max as the high-water mark.
	cu, err := CatchUp(ctx, recip, 0, 0, 100)
	if err != nil {
		t.Fatalf("catchup: %v", err)
	}
	if len(cu.Items) != 5 {
		t.Fatalf("got %d items, want 5", len(cu.Items))
	}
	if int64(cu.HighWater) != seqs[len(seqs)-1] {
		t.Fatalf("high-water = %d, want %d", cu.HighWater, seqs[len(seqs)-1])
	}
	// Strictly ascending.
	for i := 1; i < len(cu.Items); i++ {
		if cu.Items[i].Sequence <= cu.Items[i-1].Sequence {
			t.Fatal("sequences are not strictly increasing")
		}
	}
	if cu.HasMore {
		t.Fatal("unexpected HasMore")
	}
}

func TestUserEventCatchUpWindowAndPaging(t *testing.T) {
	db, recip, _ := eventFixture(t)
	ctx := context.Background()
	seqs := appendEvents(t, db, recip, 10)

	// A retained ceiling: only afterSequence < seq <= throughSequence.
	through := seqs[7]
	cu, _ := CatchUp(ctx, recip, seqs[2], through, 3)
	if !cu.HasMore {
		t.Fatal("expected HasMore with a small limit")
	}
	if len(cu.Items) != 3 || cu.Items[0].Sequence != seqs[3] {
		t.Fatalf("window start wrong: %+v", cu.Items)
	}
	// The next page continues from NextAfter and never exceeds the ceiling.
	cu2, _ := CatchUp(ctx, recip, int64(cu.NextAfter), through, 100)
	last := cu2.Items[len(cu2.Items)-1].Sequence
	if last != through {
		t.Fatalf("second page ended at %d, want ceiling %d", last, through)
	}
}

func TestUserEventCatchUpIsolationAndBounds(t *testing.T) {
	db, recip, other := eventFixture(t)
	ctx := context.Background()
	appendEvents(t, db, recip, 3)

	// The other recipient sees none of recip's events.
	cu, _ := CatchUp(ctx, other, 0, 0, 100)
	if len(cu.Items) != 0 {
		t.Fatal("recipient isolation breached in catch-up")
	}
	// Negative bounds are rejected.
	if _, err := CatchUp(ctx, recip, -1, 0, 10); err != errInvalidSequenceBounds {
		t.Fatalf("negative afterSequence = %v, want invalid", err)
	}
}
