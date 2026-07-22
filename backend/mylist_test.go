package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func myListFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	var uid string
	f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash) VALUES ('mluser','x') RETURNING id::text`).Scan(&uid)
	return f.DB, uid
}

func item(id string) AuthorizedMediaItem {
	return AuthorizedMediaItem{ID: id, Name: "T-" + id, MediaType: "Movie"}
}

func listRevision(t *testing.T, db *pgxpool.Pool, uid string) int64 {
	t.Helper()
	var rev int64
	db.QueryRow(context.Background(), `SELECT COALESCE((SELECT revision FROM media_lists WHERE user_id=$1),0)`, uid).Scan(&rev)
	return rev
}

func TestMyListAddDuplicateRemoveRevision(t *testing.T) {
	db, uid := myListFixture(t)
	ctx := context.Background()

	if err := AddToList(ctx, uid, item("a")); err != nil {
		t.Fatalf("add: %v", err)
	}
	rev1 := listRevision(t, db, uid)
	if rev1 != 1 {
		t.Fatalf("revision after add = %d, want 1", rev1)
	}
	// Duplicate add is a no-op: no new row, no revision bump.
	if err := AddToList(ctx, uid, item("a")); err != nil {
		t.Fatalf("dup add: %v", err)
	}
	if listRevision(t, db, uid) != 1 {
		t.Fatal("duplicate add bumped the revision")
	}
	var n int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM media_list_entries WHERE user_id=$1`, uid).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate add stored %d entries, want 1", n)
	}

	if err := RemoveFromList(ctx, uid, "a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if listRevision(t, db, uid) != 2 {
		t.Fatalf("revision after remove = %d, want 2", listRevision(t, db, uid))
	}
}

func TestMyListReorderAndStaleRevision(t *testing.T) {
	db, uid := myListFixture(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := AddToList(ctx, uid, item(id)); err != nil {
			t.Fatal(err)
		}
	}
	rev := listRevision(t, db, uid)

	// Move c before a: expect order c, a, b.
	before := "a"
	newRev, err := ReorderList(ctx, uid, "c", &before, rev)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if newRev != rev+1 {
		t.Fatalf("revision after reorder = %d, want %d", newRev, rev+1)
	}
	entries, _, _ := ListMyList(ctx, uid, 0, 0, false, 50)
	got := []string{entries[0].ItemID, entries[1].ItemID, entries[2].ItemID}
	if got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("order after reorder = %v, want [c a b]", got)
	}
	// Positions remain unique and contiguous.
	if entries[0].Position != 1 || entries[1].Position != 2 || entries[2].Position != 3 {
		t.Fatalf("positions not 1..3: %+v", entries)
	}

	// A stale expectedRevision conflicts.
	if _, err := ReorderList(ctx, uid, "a", nil, rev); err != errListStaleRevision {
		t.Fatalf("stale reorder = %v, want stale-revision", err)
	}
}

func TestMyListCursorStaleRevision(t *testing.T) {
	db, uid := myListFixture(t)
	ctx := context.Background()
	AddToList(ctx, uid, item("a"))
	rev := listRevision(t, db, uid)

	// A cursor bound to the current revision is fine...
	if _, _, err := ListMyList(ctx, uid, 0, rev, true, 50); err != nil {
		t.Fatalf("valid cursor: %v", err)
	}
	// ...but after a change, the old revision cursor conflicts.
	AddToList(ctx, uid, item("b"))
	if _, _, err := ListMyList(ctx, uid, 0, rev, true, 50); err != errListChanged {
		t.Fatalf("stale cursor = %v, want list-changed", err)
	}
}

func TestDeleteAccountMyList(t *testing.T) {
	db, uid := myListFixture(t)
	ctx := context.Background()
	AddToList(ctx, uid, item("a"))
	AddToList(ctx, uid, item("b"))

	if _, err := DeleteAccount(ctx, uid); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var entries, lists int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM media_list_entries WHERE user_id=$1`, uid).Scan(&entries)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM media_lists WHERE user_id=$1`, uid).Scan(&lists)
	if entries != 0 || lists != 0 {
		t.Fatalf("deletion left entries=%d lists=%d, want 0/0", entries, lists)
	}
}
