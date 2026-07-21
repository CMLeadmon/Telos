package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAdminUsersCursorPaginationCoversAll proves seek pagination returns every
// row exactly once across pages, with no duplicates or skips.
func TestAdminUsersCursorPaginationCoversAll(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	// Force a dev keyring for the codec.
	ring, _ := loadCursorKeyring("", "development")
	oldCodec := cursorCodec
	cursorCodec = &hmacCursorCodec{ring: ring}
	t.Cleanup(func() { cursorCodec = oldCodec })

	// Seed 5 users with distinct created_at ordering.
	for i := 0; i < 5; i++ {
		f.DB.Exec(ctx, `INSERT INTO users (username, password_hash, created_at) VALUES ($1,'x', NOW() + ($2 || ' seconds')::interval)`,
			"user"+itoa(i), itoa(i))
	}

	actor := &UserContext{ID: "admin", Roles: []string{"Owner"}, Permissions: []string{"manage_members"}}
	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		url := "/api/v1/admin/users?limit=2"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		r := httptest.NewRequest("GET", url, nil)
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, actor))
		rec := httptest.NewRecorder()
		handleAdminListUsers(rec, r)
		if rec.Code != 200 {
			t.Fatalf("page %d status %d", pages, rec.Code)
		}
		var page Page[AdminUserResponse]
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, u := range page.Items {
			seen[u.ID]++
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != 5 {
		t.Fatalf("saw %d distinct users, want 5", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("user %s returned %d times", id, n)
		}
	}
	_ = time.Now
}
