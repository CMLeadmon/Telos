package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// jellyfinResolverFake serves the two endpoints the resolver uses, shaped like a
// Jellyfin 10.11 server: an item's ancestors are its physical folders and the
// aggregate root, never the library (CollectionFolder) itself.
func jellyfinResolverFake(t *testing.T, ancestors map[string]string, virtualFoldersStatus *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "resolver-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/Library/VirtualFolders":
			if status := virtualFoldersStatus.Load(); status != 0 {
				w.WriteHeader(int(status))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Name": "Movies", "ItemId": "lib-movies", "Locations": []string{"/data/media/movies"}},
				{"Name": "Music", "ItemId": "lib-music", "Locations": []string{"/data/media/music/"}},
			})
		case strings.HasPrefix(r.URL.Path, "/Items/") && strings.HasSuffix(r.URL.Path, "/Ancestors"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Items/"), "/Ancestors")
			folder, ok := ancestors[id]
			if !ok {
				_ = json.NewEncoder(w).Encode([]any{})
				return
			}
			chain := []map[string]string{
				{"Id": "folder-" + id, "Type": "Folder", "Name": "folder", "Path": folder},
				{"Id": "root", "Type": "AggregateFolder", "Name": "root", "Path": "/config/root"},
			}
			if strings.HasPrefix(folder, "collection:") {
				// Older servers list the library itself.
				chain = []map[string]string{
					{"Id": strings.TrimPrefix(folder, "collection:"), "Type": "CollectionFolder", "Name": "Movies"},
					{"Id": "root", "Type": "AggregateFolder", "Name": "root"},
				}
			}
			_ = json.NewEncoder(w).Encode(chain)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func useJellyfinResolverFake(t *testing.T, server *httptest.Server) {
	t.Helper()
	previous := jellyfinBaseURL
	jellyfinBaseURL = server.URL
	t.Setenv("JELLYFIN_ADMIN_TOKEN", "resolver-token")
	jellyfinLibraryLocations.reset()
	t.Cleanup(func() {
		jellyfinBaseURL = previous
		jellyfinLibraryLocations.reset()
	})
}

func TestJellyfinAuthorizationFindsTheLibraryOfAnItemInItsDirectory(t *testing.T) {
	var status atomic.Int32
	server := jellyfinResolverFake(t, map[string]string{
		"aaaaaaaa11111111aaaaaaaa11111111": "/data/media/movies",
		"bbbbbbbb22222222bbbbbbbb22222222": "/data/media/music",
		"cccccccc33333333cccccccc33333333": "/data/media/other",
		"dddddddd44444444dddddddd44444444": "collection:lib-movies",
	}, &status)
	useJellyfinResolverFake(t, server)

	authorizer, err := NewJellyfinAuthorizer(jellyfinAPIResolver{}, []string{"lib-movies"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := authorizer.AuthorizeItems(context.Background(), []string{
		"aaaaaaaa11111111aaaaaaaa11111111", "bbbbbbbb22222222bbbbbbbb22222222",
		"cccccccc33333333cccccccc33333333", "dddddddd44444444dddddddd44444444",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := items["aaaaaaaa11111111aaaaaaaa11111111"].LibraryID; got != "lib-movies" {
		t.Errorf("a movie in the library's directory authorized as %q, want lib-movies", got)
	}
	if _, ok := items["bbbbbbbb22222222bbbbbbbb22222222"]; ok {
		t.Error("an item in a library that is not exposed was authorized")
	}
	if _, ok := items["cccccccc33333333cccccccc33333333"]; ok {
		t.Error("an item outside every library directory was authorized")
	}
	if got := items["dddddddd44444444dddddddd44444444"].LibraryID; got != "lib-movies" {
		t.Errorf("a server that lists the library as an ancestor stopped working: %q", got)
	}
}

func TestJellyfinAuthorizationFailsClosedWhenLibrariesCannotBeListed(t *testing.T) {
	var status atomic.Int32
	server := jellyfinResolverFake(t, map[string]string{"aaaaaaaa11111111aaaaaaaa11111111": "/data/media/movies"}, &status)
	useJellyfinResolverFake(t, server)
	authorizer, err := NewJellyfinAuthorizer(jellyfinAPIResolver{}, []string{"lib-movies"})
	if err != nil {
		t.Fatal(err)
	}

	status.Store(http.StatusInternalServerError)
	items, err := authorizer.AuthorizeItems(context.Background(), []string{"aaaaaaaa11111111aaaaaaaa11111111"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("with no library listing nothing may be authorized, got %v", items)
	}

	// A recovered listing authorizes again once the short negative cache lapses;
	// a later outage keeps serving the last good map rather than un-authorizing.
	status.Store(0)
	authorizer.Invalidate()
	items, _ = authorizer.AuthorizeItems(context.Background(), []string{"aaaaaaaa11111111aaaaaaaa11111111"})
	if len(items) != 1 {
		t.Fatalf("the recovered listing did not authorize the movie: %v", items)
	}
	status.Store(http.StatusInternalServerError)
	authorizer.Invalidate()
	items, _ = authorizer.AuthorizeItems(context.Background(), []string{"aaaaaaaa11111111aaaaaaaa11111111"})
	if len(items) != 1 {
		t.Fatalf("a transient listing failure dropped the last good library map: %v", items)
	}
}
