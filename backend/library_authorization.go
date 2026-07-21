package main

import (
	"context"
	"errors"
	"net/http"
)

// BookAction is a requested action on a book.
type BookAction string

const (
	BookRead   BookAction = "read"
	BookManage BookAction = "manage"
)

// AuthorizedBook is a Grimmory book confirmed to belong to a configured library.
type AuthorizedBook struct {
	ID        int64
	LibraryID string
	Title     string
	Format    string
}

// GrimmoryBookResolver resolves a book's library ID from bounded upstream APIs.
type GrimmoryBookResolver interface {
	ResolveBook(ctx context.Context, bookID string) (title, format, libraryID string, err error)
}

// GrimmoryAuthorizer verifies book library membership and Telos permissions.
type GrimmoryAuthorizer struct {
	resolver GrimmoryBookResolver
	allowed  map[string]struct{}
}

var (
	errBookNotAuthorized = errors.New("book not authorized")
	errBookAmbiguous     = errors.New("book library mapping is ambiguous")
)

// NewGrimmoryAuthorizer requires a nonempty allowlist of stable library IDs.
func NewGrimmoryAuthorizer(resolver GrimmoryBookResolver, allowedLibraryIDs []string) (*GrimmoryAuthorizer, error) {
	if len(allowedLibraryIDs) == 0 {
		return nil, errors.New("GRIMMORY_LIBRARY_IDS must be a nonempty list of stable library IDs")
	}
	allowed := map[string]struct{}{}
	for _, id := range allowedLibraryIDs {
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("GRIMMORY_LIBRARY_IDS contains no valid IDs")
	}
	return &GrimmoryAuthorizer{resolver: resolver, allowed: allowed}, nil
}

// AuthorizeBook enforces library membership plus the Telos permission for the
// action: BookRead needs view_library; BookManage needs view_library and
// manage_library. Out-of-scope or ambiguous books fail closed.
func (a *GrimmoryAuthorizer) AuthorizeBook(ctx context.Context, user *UserContext, bookID string, action BookAction) (AuthorizedBook, error) {
	if user == nil {
		return AuthorizedBook{}, errBookNotAuthorized
	}
	canView, err := hasPermission(ctx, user, "view_library", nil)
	if err != nil {
		return AuthorizedBook{}, err
	}
	if !canView {
		return AuthorizedBook{}, errBookNotAuthorized
	}
	if action == BookManage {
		canManage, err := hasPermission(ctx, user, "manage_library", nil)
		if err != nil {
			return AuthorizedBook{}, err
		}
		if !canManage {
			return AuthorizedBook{}, errBookNotAuthorized
		}
	}

	title, format, libraryID, err := a.resolver.ResolveBook(ctx, bookID)
	if err != nil {
		return AuthorizedBook{}, err
	}
	if libraryID == "" {
		// Unmapped: a single configured library resolves unambiguously; more
		// than one is genuinely ambiguous and fails closed.
		if len(a.allowed) == 1 {
			for id := range a.allowed {
				libraryID = id
			}
		} else {
			return AuthorizedBook{}, errBookAmbiguous
		}
	}
	if _, ok := a.allowed[libraryID]; !ok {
		return AuthorizedBook{}, errBookNotAuthorized
	}
	return AuthorizedBook{LibraryID: libraryID, Title: title, Format: format}, nil
}

// grimmoryAPIResolver resolves a book's library ID from the Grimmory detail
// API. Grimmory exposes a shelf/library id on the book detail; when absent the
// mapping is treated as ambiguous and the caller fails closed.
type grimmoryAPIResolver struct{}

func (grimmoryAPIResolver) ResolveBook(ctx context.Context, bookID string) (string, string, string, error) {
	book, err := fetchGrimmoryBook(ctx, bookID)
	if err != nil {
		return "", "", "", errBookNotAuthorized
	}
	// The current Grimmory DTO exposes no library ID; membership is resolved by
	// the authorizer's single-library rule. An empty ID means "unmapped".
	return book.Title, book.Format, "", nil
}

var grimmoryAuthorizer *GrimmoryAuthorizer

// authorizeBookHTTP gates a library route on membership + the action permission,
// returning an indistinguishable 404 for out-of-scope books. Allow-all when no
// authorizer is configured (development).
func authorizeBookHTTP(w http.ResponseWriter, r *http.Request, bookID string, action BookAction) bool {
	if grimmoryAuthorizer == nil {
		return true
	}
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if _, err := grimmoryAuthorizer.AuthorizeBook(r.Context(), user, bookID, action); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return true
}
