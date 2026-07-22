package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func notifFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	mk := func(name string) string {
		var id string
		if err := f.DB.QueryRow(context.Background(), `INSERT INTO users (username, password_hash) VALUES ($1,'x') RETURNING id::text`, name).Scan(&id); err != nil {
			t.Fatalf("user: %v", err)
		}
		return id
	}
	return f.DB, mk("recipient"), mk("actor")
}

func createNotif(t *testing.T, db *pgxpool.Pool, in NotificationInput) Notification {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	n, err := CreateNotification(ctx, tx, in)
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return n
}

func TestNotificationCreateAndIdempotency(t *testing.T) {
	db, recip, actor := notifFixture(t)
	ctx := context.Background()
	in := NotificationInput{
		RecipientID: recip, ActorID: actor, Kind: NotifyMention,
		ResourceType: "message", ResourceID: "m1", IdempotencyKey: "mention:m1:" + recip,
		Payload: json.RawMessage(`{"preview":"hi"}`),
	}
	n1 := createNotif(t, db, in)
	if n1.EventSequence == 0 {
		t.Fatal("notification not linked to an event sequence")
	}

	// Replay with the same key returns the same notification and creates no
	// duplicate notification, event, or outbox row.
	n2 := createNotif(t, db, in)
	if n2.ID != n1.ID {
		t.Fatalf("replay produced a new notification %s vs %s", n2.ID, n1.ID)
	}
	var notifs, events, outbox int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1`, recip).Scan(&notifs)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM user_events WHERE recipient_id=$1`, recip).Scan(&events)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE topic=$1`, "telos:user:"+recip).Scan(&outbox)
	if notifs != 1 || events != 1 || outbox != 1 {
		t.Fatalf("idempotency leaked: notifs=%d events=%d outbox=%d, want 1/1/1", notifs, events, outbox)
	}
}

func TestNotificationRecipientIsolation(t *testing.T) {
	db, recip, actor := notifFixture(t)
	ctx := context.Background()
	var other string
	db.QueryRow(ctx, `INSERT INTO users (username,password_hash) VALUES ('other','x') RETURNING id::text`).Scan(&other)

	n := createNotif(t, db, NotificationInput{RecipientID: recip, ActorID: actor, Kind: NotifyMention, IdempotencyKey: "k1"})

	// Another user cannot see or mark it.
	items, _ := ListNotifications(ctx, other, time.Time{}, "", false, 50)
	if len(items) != 0 {
		t.Fatal("recipient isolation breached in listing")
	}
	if err := MarkRead(ctx, other, n.ID); err != errNotificationNotFound {
		t.Fatalf("cross-user MarkRead = %v, want not-found", err)
	}
}

func TestNotificationUnreadAndReadIdempotency(t *testing.T) {
	db, recip, actor := notifFixture(t)
	ctx := context.Background()
	n := createNotif(t, db, NotificationInput{RecipientID: recip, ActorID: actor, Kind: NotifyMention, IdempotencyKey: "k1"})
	createNotif(t, db, NotificationInput{RecipientID: recip, ActorID: actor, Kind: NotifyMention, IdempotencyKey: "k2"})

	if c, _ := UnreadCount(ctx, recip); c != 2 {
		t.Fatalf("unread = %d, want 2", c)
	}
	if err := MarkRead(ctx, recip, n.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	// Second read is a stable no-op.
	if err := MarkRead(ctx, recip, n.ID); err != nil {
		t.Fatalf("repeat read: %v", err)
	}
	if c, _ := UnreadCount(ctx, recip); c != 1 {
		t.Fatalf("unread after one read = %d, want 1", c)
	}
	if updated, _ := MarkAllRead(ctx, recip); updated != 1 {
		t.Fatalf("mark-all updated %d, want 1 remaining unread", updated)
	}
	if c, _ := UnreadCount(ctx, recip); c != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", c)
	}
}

func TestDeleteAccountNotificationAndUserEvent(t *testing.T) {
	db, recip, actor := notifFixture(t)
	ctx := context.Background()
	createNotif(t, db, NotificationInput{RecipientID: recip, ActorID: actor, Kind: NotifyMention, IdempotencyKey: "k1"})

	if _, err := DeleteAccount(ctx, recip); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	var notifs, events int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1`, recip).Scan(&notifs)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM user_events WHERE recipient_id=$1`, recip).Scan(&events)
	if notifs != 0 || events != 0 {
		t.Fatalf("deletion left notifs=%d events=%d, want 0/0", notifs, events)
	}
}
