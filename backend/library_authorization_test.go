package main

import (
	"context"
	"testing"
)

type fakeGrimmoryResolver struct {
	books map[string]struct{ title, format, lib string }
}

func (f *fakeGrimmoryResolver) ResolveBook(_ context.Context, id string) (string, string, string, error) {
	b, ok := f.books[id]
	if !ok {
		return "", "", "", errBookNotAuthorized
	}
	return b.title, b.format, b.lib, nil
}

func TestGrimmoryAuthorizer(t *testing.T) {
	resolver := &fakeGrimmoryResolver{books: map[string]struct{ title, format, lib string }{
		"1": {"In Library", "epub", "lib-allowed"},
		"2": {"Other Library", "epub", "lib-forbidden"},
		"3": {"No Library", "epub", ""}, // ambiguous
	}}
	// Two configured libraries so an unmapped (empty) library ID is genuinely
	// ambiguous rather than resolving to a single library.
	a, err := NewGrimmoryAuthorizer(resolver, []string{"lib-allowed", "lib-second"})
	if err != nil {
		t.Fatal(err)
	}

	reader := &UserContext{ID: "u1", Roles: []string{"Member"}}     // view_library
	manager := &UserContext{ID: "u2", Roles: []string{"Moderator"}} // view + manage_library
	nobody := &UserContext{ID: "u3", Roles: []string{}}

	// This test does not hit the DB; hasPermission needs dbPool. Skip if unset.
	if dbPool == nil {
		t.Skip("requires dbPool for permission checks (run via integration harness)")
	}

	// Reader can read an in-library book.
	if _, err := a.AuthorizeBook(context.Background(), reader, "1", BookRead); err != nil {
		t.Fatalf("reader denied in-library read: %v", err)
	}
	// Reader cannot manage (needs manage_library).
	if _, err := a.AuthorizeBook(context.Background(), reader, "1", BookManage); err == nil {
		t.Fatal("read-only user allowed to manage")
	}
	// Manager can manage.
	if _, err := a.AuthorizeBook(context.Background(), manager, "1", BookManage); err != nil {
		t.Fatalf("manager denied manage: %v", err)
	}
	// Out-of-library book denied.
	if _, err := a.AuthorizeBook(context.Background(), reader, "2", BookRead); err == nil {
		t.Fatal("out-of-library book authorized")
	}
	// Ambiguous mapping fails closed.
	if _, err := a.AuthorizeBook(context.Background(), reader, "3", BookRead); err == nil {
		t.Fatal("ambiguous book authorized")
	}
	// No permission denied.
	if _, err := a.AuthorizeBook(context.Background(), nobody, "1", BookRead); err == nil {
		t.Fatal("permissionless user authorized")
	}
}

func TestGrimmoryAuthorizerRequiresLibraries(t *testing.T) {
	if _, err := NewGrimmoryAuthorizer(&fakeGrimmoryResolver{}, nil); err == nil {
		t.Fatal("empty library allowlist accepted")
	}
}
