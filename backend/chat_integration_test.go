package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const generalChannel = "00000000-0000-0000-0000-000000000001"
const devChannel = "00000000-0000-0000-0000-000000000002"

func chatFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	// Reset truncates the seeded channels; re-insert the two this test uses.
	for _, id := range []string{generalChannel, devChannel} {
		f.DB.Exec(context.Background(),
			`INSERT INTO channels (id, name, type) VALUES ($1, $2, 'text') ON CONFLICT (id) DO NOTHING`,
			id, "chan-"+id[:8])
	}
	mk := func(n string) string {
		var id string
		f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash,active) VALUES ($1,'x',TRUE) RETURNING id::text`, n).Scan(&id)
		return id
	}
	return f.DB, mk("author"), mk("replier")
}

func mustCreate(t *testing.T, db *pgxpool.Pool, channelID, authorID, content, mutID string, root *string) (string, bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	id, _, createdNew, err := createChatMessageTx(ctx, tx, channelID, authorID, content, mutID, root, messageEmbedInput{})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id, createdNew
}

func TestThreadRepliesRejectSecondLevelAndCrossChannel(t *testing.T) {
	db, author, replier := chatFixture(t)
	ctx := context.Background()
	root, _ := mustCreate(t, db, generalChannel, author, "root", "", nil)
	reply, _ := mustCreate(t, db, generalChannel, replier, "a reply", "", &root)

	// A reply-to-reply is rejected by the database trigger.
	tx, _ := db.Begin(ctx)
	defer tx.Rollback(ctx)
	if _, _, _, err := createChatMessageTx(ctx, tx, generalChannel, author, "nested", "", &reply, messageEmbedInput{}); err == nil {
		t.Fatal("reply-to-reply was accepted")
	}
	tx.Rollback(ctx)

	// A root from another channel cannot anchor a reply here.
	tx2, _ := db.Begin(ctx)
	defer tx2.Rollback(ctx)
	if _, _, _, err := createChatMessageTx(ctx, tx2, devChannel, author, "cross", "", &root, messageEmbedInput{}); err == nil {
		t.Fatal("cross-channel reply was accepted")
	}
}

func TestChannelRootHistoryExcludesRepliesAndCountsThem(t *testing.T) {
	db, author, replier := chatFixture(t)
	root, _ := mustCreate(t, db, generalChannel, author, "root", "", nil)
	mustCreate(t, db, generalChannel, replier, "r1", "", &root)
	mustCreate(t, db, generalChannel, replier, "r2", "", &root)

	roots, err := ListChannelRoots(context.Background(), generalChannel, time.Time{}, "", false, 50)
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	var found *ChatMessage
	for i := range roots {
		if roots[i].ThreadRootID != nil {
			t.Fatal("a reply leaked into root history")
		}
		if roots[i].ID == root {
			found = &roots[i]
		}
	}
	if found == nil || found.ReplyCount != 2 {
		t.Fatalf("root reply metadata wrong: %+v", found)
	}
	if found.LastReplyBy == nil || found.LastReplyBy.ID != replier {
		t.Fatalf("last-reply-by wrong: %+v", found.LastReplyBy)
	}
}

func TestDuplicateMessageMutationReturnsOne(t *testing.T) {
	db, author, _ := chatFixture(t)
	id1, new1 := mustCreate(t, db, generalChannel, author, "hello", "mut-1", nil)
	id2, new2 := mustCreate(t, db, generalChannel, author, "hello again", "mut-1", nil)
	if id1 != id2 {
		t.Fatalf("duplicate mutation produced two messages: %s vs %s", id1, id2)
	}
	if !new1 || new2 {
		t.Fatalf("createdNew flags wrong: %v %v", new1, new2)
	}
	var n int
	db.QueryRow(context.Background(), `SELECT COUNT(*) FROM messages WHERE user_id=$1 AND client_mutation_id='mut-1'`, author).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate mutation stored %d messages, want 1", n)
	}
}

func TestChannelChangeCatchUpForwardWindow(t *testing.T) {
	db, author, _ := chatFixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		mustCreate(t, db, generalChannel, author, "m", "", nil)
	}
	cu, err := CatchUpChannelChanges(ctx, generalChannel, 0, 0, 100)
	if err != nil {
		t.Fatalf("catchup: %v", err)
	}
	if len(cu.Items) < 5 {
		t.Fatalf("expected >=5 changes, got %d", len(cu.Items))
	}
	for i := 1; i < len(cu.Items); i++ {
		if cu.Items[i].Sequence <= cu.Items[i-1].Sequence {
			t.Fatal("change sequences not strictly increasing")
		}
	}
	if int64(cu.HighWater) != cu.Items[len(cu.Items)-1].Sequence {
		t.Fatalf("high-water %d != last item seq", cu.HighWater)
	}
	// A window strictly after the first item.
	after := cu.Items[0].Sequence
	cu2, _ := CatchUpChannelChanges(ctx, generalChannel, after, int64(cu.HighWater), 100)
	if len(cu2.Items) == 0 || cu2.Items[0].Sequence <= after {
		t.Fatalf("windowed catch-up wrong: %+v", cu2.Items)
	}
}

func TestReplyNotificationToRootAuthor(t *testing.T) {
	db, author, replier := chatFixture(t)
	ctx := context.Background()
	root, _ := mustCreate(t, db, generalChannel, author, "root", "", nil)
	mustCreate(t, db, generalChannel, replier, "thanks", "", &root)

	var kind string
	err := db.QueryRow(ctx, `SELECT kind FROM notifications WHERE user_id=$1 AND kind='thread_reply'`, author).Scan(&kind)
	if err == pgx.ErrNoRows {
		t.Fatal("root author did not receive a thread_reply notification")
	}
	if err != nil {
		t.Fatalf("notif query: %v", err)
	}

	// A @mention notifies the mentioned user.
	mustCreate(t, db, generalChannel, replier, "hey @author look", "", nil)
	var mentions int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND kind='mention'`, author).Scan(&mentions)
	if mentions != 1 {
		t.Fatalf("mention notifications = %d, want 1", mentions)
	}
}

func TestMessageCursorStability(t *testing.T) {
	db, author, _ := chatFixture(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		mustCreate(t, db, generalChannel, author, "m", "", nil)
	}
	first, _ := ListChannelRoots(ctx, generalChannel, time.Time{}, "", false, 3)
	if len(first) != 3 {
		t.Fatalf("first page %d, want 3", len(first))
	}
	last := first[2]
	second, _ := ListChannelRoots(ctx, generalChannel, last.CreatedAt, last.ID, true, 3)
	// No overlap between pages.
	for _, a := range first {
		for _, b := range second {
			if a.ID == b.ID {
				t.Fatal("cursor pages overlap")
			}
		}
	}
}
