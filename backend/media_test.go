package main

import (
	"context"
	"sync/atomic"
	"testing"
)

type fakeJellyfinResolver struct {
	items map[string]resolvedItem
	calls atomic.Int32
}

func (f *fakeJellyfinResolver) ResolveItems(_ context.Context, ids []string) (map[string]resolvedItem, error) {
	f.calls.Add(1)
	out := map[string]resolvedItem{}
	for _, id := range ids {
		if ri, ok := f.items[id]; ok {
			out[id] = ri
		}
	}
	return out, nil
}

func TestJellyfinAuthorizer(t *testing.T) {
	resolver := &fakeJellyfinResolver{items: map[string]resolvedItem{
		"inlib":  {Name: "Movie", MediaType: "Video", AncestorIDs: []string{"root", "lib-allowed"}},
		"outlib": {Name: "Other", MediaType: "Video", AncestorIDs: []string{"root", "lib-forbidden"}},
	}}
	a, err := NewJellyfinAuthorizer(resolver, []string{"lib-allowed"})
	if err != nil {
		t.Fatal(err)
	}

	// In-library item authorizes.
	item, err := a.AuthorizeItem(context.Background(), "inlib")
	if err != nil || item.LibraryID != "lib-allowed" {
		t.Fatalf("in-library item denied: %v (%+v)", err, item)
	}
	// Out-of-library item is denied indistinguishably.
	if _, err := a.AuthorizeItem(context.Background(), "outlib"); err == nil {
		t.Fatal("out-of-library item authorized")
	}
	// Unknown item denied.
	if _, err := a.AuthorizeItem(context.Background(), "ghost"); err == nil {
		t.Fatal("unknown item authorized")
	}

	// Empty library allowlist is rejected at construction.
	if _, err := NewJellyfinAuthorizer(resolver, nil); err == nil {
		t.Fatal("empty allowlist accepted")
	}
}

func TestJellyfinAuthorizerBatchIsBounded(t *testing.T) {
	items := map[string]resolvedItem{}
	ids := make([]string, 50)
	for i := 0; i < 50; i++ {
		id := "item" + itoa(i)
		ids[i] = id
		items[id] = resolvedItem{Name: id, AncestorIDs: []string{"lib-allowed"}}
	}
	resolver := &fakeJellyfinResolver{items: items}
	a, _ := NewJellyfinAuthorizer(resolver, []string{"lib-allowed"})

	// A 50-item batch (with duplicates) uses ONE upstream call, not N+1.
	dup := append(append([]string{}, ids...), ids...) // 100 with dupes
	res, err := a.AuthorizeItems(context.Background(), dup)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 50 {
		t.Fatalf("authorized %d items, want 50", len(res))
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1 (no N+1)", resolver.calls.Load())
	}

	// A second call is served from the positive cache — no new upstream call.
	a.AuthorizeItems(context.Background(), ids)
	if resolver.calls.Load() != 1 {
		t.Fatalf("cache miss: resolver called %d times", resolver.calls.Load())
	}
}
