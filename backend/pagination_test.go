package main

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func testCodec(t *testing.T) *hmacCursorCodec {
	t.Helper()
	k1 := sha256.Sum256([]byte("key-one"))
	k2 := sha256.Sum256([]byte("key-two-retiring"))
	ring := &cursorKeyring{
		activeID: "k1",
		keys:     map[string][]byte{"k1": k1[:], "k2": k2[:]},
	}
	if err := ring.validate(); err != nil {
		t.Fatal(err)
	}
	return &hmacCursorCodec{ring: ring}
}

func sampleCursor() PageCursor {
	return PageCursor{
		Sort:     "created_desc",
		Values:   []CursorValue{{Kind: "time", Value: "2026-07-20T00:00:00Z"}, {Kind: "uuid", Value: "abc"}},
		ID:       "abc",
		IssuedAt: time.Now(),
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := testCodec(t)
	tok, err := c.Encode("admin.users", sampleCursor())
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode("admin.users", tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "admin.users" || len(got.Values) != 2 || got.ID != "abc" {
		t.Fatalf("decoded wrong: %+v", got)
	}
}

func TestCursorRejections(t *testing.T) {
	c := testCodec(t)
	valid, _ := c.Encode("admin.users", sampleCursor())

	t.Run("wrong scope", func(t *testing.T) {
		if _, err := c.Decode("admin.invites", valid); err == nil {
			t.Fatal("cross-scope cursor accepted")
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		parts := strings.SplitN(valid, ".", 2)
		if _, err := c.Decode("admin.users", "eyJ4IjoxfQ."+parts[1]); err == nil {
			t.Fatal("tampered cursor accepted")
		}
	})
	t.Run("garbage", func(t *testing.T) {
		if _, err := c.Decode("admin.users", "not-a-cursor"); err == nil {
			t.Fatal("garbage accepted")
		}
	})
	t.Run("expired", func(t *testing.T) {
		old := sampleCursor()
		old.IssuedAt = time.Now().Add(-25 * time.Hour)
		tok, _ := c.Encode("admin.users", old)
		if _, err := c.Decode("admin.users", tok); err == nil {
			t.Fatal("expired cursor accepted")
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		other := testCodec(t)
		other.ring.keys = map[string][]byte{"kX": other.ring.keys["k1"]}
		other.ring.activeID = "kX"
		tok, _ := other.Encode("admin.users", sampleCursor())
		if _, err := c.Decode("admin.users", tok); err == nil {
			t.Fatal("cursor signed by unknown key accepted")
		}
	})
}

func TestCursorRetiringKeyStillDecodes(t *testing.T) {
	c := testCodec(t)
	// Encode with k1 (active), then retire k1 by making k2 active; k1 remains
	// present for decode.
	tok, _ := c.Encode("admin.users", sampleCursor())
	c.ring.activeID = "k2"
	if _, err := c.Decode("admin.users", tok); err != nil {
		t.Fatalf("retiring-key cursor rejected: %v", err)
	}
}

func TestKeyringValidation(t *testing.T) {
	short := &cursorKeyring{activeID: "a", keys: map[string][]byte{"a": []byte("tooshort")}}
	if err := short.validate(); err == nil {
		t.Fatal("short key accepted")
	}
	noActive := &cursorKeyring{activeID: "missing", keys: map[string][]byte{"a": make([]byte, 32)}}
	if err := noActive.validate(); err == nil {
		t.Fatal("missing active key accepted")
	}
	dup := &cursorKeyring{activeID: "a", keys: map[string][]byte{"a": make([]byte, 32), "b": make([]byte, 32)}}
	if err := dup.validate(); err == nil {
		t.Fatal("bytewise-identical keys accepted")
	}
}

func TestResolvePageRequest(t *testing.T) {
	if _, _, err := resolvePageRequest("not.registered", "", "", 50); err == nil {
		t.Fatal("unregistered scope accepted")
	}
	if _, _, err := resolvePageRequest("channels", "", "", 50); err == nil {
		t.Fatal("capped scope accepted as cursor list")
	}
	req, _, err := resolvePageRequest("admin.users", "", "", 500)
	if err != nil || req.Limit != 100 {
		t.Fatalf("limit not clamped to 100: %+v err=%v", req, err)
	}
	if _, _, err := resolvePageRequest("admin.users", "bogus_sort", "", 50); err == nil {
		t.Fatal("unknown sort accepted")
	}
}

func TestListPolicyRegistryCompleteness(t *testing.T) {
	// Every list surface must be registered (mirrors api-list-inventory.md).
	required := []string{
		"chat.history", "admin.users", "admin.invites", "admin.sessions",
		"channel.members", "channels", "roles", "permissions",
		"message.reactions", "channel.pins",
		"search.users", "search.channels", "search.files", "search.media", "search.library",
	}
	for _, s := range required {
		if _, ok := ListPolicyFor(s); !ok {
			t.Errorf("list scope %q is not registered", s)
		}
	}
}
