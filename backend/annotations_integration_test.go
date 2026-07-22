package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func annotationFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	mk := func(n string) string {
		var id string
		f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash,active) VALUES ($1,'x',TRUE) RETURNING id::text`, n).Scan(&id)
		return id
	}
	return f.DB, mk("owner"), mk("reader")
}

var epubLoc = json.RawMessage(`{"kind":"epub","cfi":"epubcfi(/6/4!/4)"}`)

func TestAnnotationPrivateDefaultAndIsolation(t *testing.T) {
	_, owner, reader := annotationFixture(t)
	ctx := context.Background()

	// A default annotation is private and invisible to other users.
	a, err := CreateAnnotation(ctx, 42, owner, "", epubLoc, "selected", "note")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Visibility != "private" {
		t.Fatalf("default visibility = %q, want private", a.Visibility)
	}
	got, _ := ListAnnotations(ctx, 42, reader, 100)
	if len(got) != 0 {
		t.Fatal("cross-user private annotation leaked")
	}
	mine, _ := ListAnnotations(ctx, 42, owner, 100)
	if len(mine) != 1 {
		t.Fatalf("owner sees %d, want 1", len(mine))
	}

	// Transitioning to community makes it visible to everyone.
	if err := UpdateAnnotation(ctx, a.ID, owner, "community", "note2"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = ListAnnotations(ctx, 42, reader, 100)
	if len(got) != 1 || got[0].Visibility != "community" {
		t.Fatalf("community annotation not enumerated: %+v", got)
	}

	// A non-owner cannot mutate it.
	if err := UpdateAnnotation(ctx, a.ID, reader, "private", "hijack"); err != errAnnotationNotFound {
		t.Fatalf("cross-user update = %v, want not-found", err)
	}
}

func TestAnnotationRepliesOnlyOnCommunityAndNotify(t *testing.T) {
	db, owner, reader := annotationFixture(t)
	ctx := context.Background()
	priv, _ := CreateAnnotation(ctx, 42, owner, "private", epubLoc, "s", "n")

	// No reply on a private annotation.
	if _, err := CreateReply(ctx, priv.ID, reader, "hi"); err != errReplyOnlyCommunity {
		t.Fatalf("reply on private = %v, want only-community", err)
	}

	comm, _ := CreateAnnotation(ctx, 42, owner, "community", epubLoc, "s", "n")
	reply, err := CreateReply(ctx, comm.ID, reader, "great highlight")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	// The owner is notified (annotation_reply); the payload holds no bodies.
	var kind string
	var payload []byte
	if err := db.QueryRow(ctx, `SELECT kind, payload FROM notifications WHERE user_id=$1 AND kind='annotation_reply'`, owner).Scan(&kind, &payload); err != nil {
		t.Fatalf("owner not notified: %v", err)
	}
	if string(payload) == "" || strings.Contains(string(payload), "great highlight") {
		t.Fatalf("notification payload leaked the reply body: %s", payload)
	}
	// Replies list the new reply.
	replies, _ := ListReplies(ctx, comm.ID, 100)
	if len(replies) != 1 || replies[0].ID != reply.ID {
		t.Fatalf("reply listing wrong: %+v", replies)
	}
}

func TestModerateAnnotationRequiresPermission(t *testing.T) {
	db, owner, reader := annotationFixture(t)
	ctx := context.Background()
	comm, _ := CreateAnnotation(ctx, 42, owner, "community", epubLoc, "s", "n")

	// A plain reader cannot moderate.
	nobody := &UserContext{ID: reader, Roles: []string{}}
	if err := DeleteAnnotation(ctx, comm.ID, nobody); err != errAnnotationDenied {
		t.Fatalf("non-moderator delete = %v, want denied", err)
	}
	// An Owner-role moderator can (Owner bypasses permission checks).
	mod := &UserContext{ID: reader, Roles: []string{"Owner"}}
	if err := DeleteAnnotation(ctx, comm.ID, mod); err != nil {
		t.Fatalf("moderator delete: %v", err)
	}
	var n int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE id=$1`, comm.ID).Scan(&n)
	if n != 0 {
		t.Fatal("moderated annotation was not removed")
	}
}

func TestDeleteAccountAnnotations(t *testing.T) {
	db, owner, _ := annotationFixture(t)
	ctx := context.Background()
	CreateAnnotation(ctx, 42, owner, "private", epubLoc, "s", "n")
	CreateAnnotation(ctx, 42, owner, "community", epubLoc, "s", "n")

	if _, err := DeleteAccount(ctx, owner); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var priv, comm int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE user_id=$1 AND visibility='private'`, owner).Scan(&priv)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE user_id=$1 AND visibility='community'`, owner).Scan(&comm)
	if priv != 0 {
		t.Fatalf("private annotations retained after deletion: %d", priv)
	}
	if comm != 1 {
		t.Fatalf("community annotation should be retained (anonymized), got %d", comm)
	}
}
