package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeGrimmory returns a test server that accepts login for gw/pw and
// serves authed endpoints, counting logins.
func fakeGrimmory(t *testing.T, logins *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Username != "gw" || body.Password != "pw" {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		*logins++
		json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "tok-1", "expires": 7200, "refreshToken": "r",
		})
	})
	mux.HandleFunc("GET /api/v1/books", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"addedOn":"2026-07-12T20:09:10Z","id":1,"libraryId":1,"libraryName":"Books",
			"metadata":{"bookId":1,"title":"Pride and Prejudice","language":"en",
				"authors":["Jane Austen"],"categories":["Love stories","England -- Fiction"]},
			"primaryFile":{"bookType":"EPUB","extension":"epub","fileName":"pp.epub","fileSizeKb":24253}}]`))
	})
	mux.HandleFunc("GET /api/v1/media/book/1/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Grimmory mislabels JPEG bytes as JSON — reproduce that.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("\xff\xd8\xff\xe0fake-jpeg"))
	})
	mux.HandleFunc("GET /api/v1/books/1/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Write([]byte("PK\x03\x04fake-epub"))
	})
	return httptest.NewServer(mux)
}

func setupGrimmoryTest(t *testing.T) func() {
	t.Helper()
	logins := 0
	srv := fakeGrimmory(t, &logins)
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	return func() { grimmoryBaseURL = oldURL; srv.Close() }
}

func TestHandleLibraryBooksTranslates(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/books", nil)
	rec := httptest.NewRecorder()
	handleLibraryBooks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var books []LibraryBook
	if err := json.Unmarshal(rec.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].Title != "Pride and Prejudice" ||
		books[0].Format != "EPUB" || books[0].Authors[0] != "Jane Austen" {
		t.Fatalf("bad translation: %+v", books)
	}
}

func TestHandleLibraryFacetsDerives(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/facets", nil)
	rec := httptest.NewRecorder()
	handleLibraryFacets(rec, req)
	var f LibraryFacets
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Authors) != 1 || f.Authors[0].Value != "Jane Austen" || f.Authors[0].Count != 1 {
		t.Fatalf("bad authors facet: %+v", f.Authors)
	}
	if len(f.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %+v", f.Categories)
	}
}

func TestHandleLibraryBookCoverForcesJPEG(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/books/1/cover", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	handleLibraryBookCover(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("expected image/jpeg (upstream mislabels), got %q", ct)
	}
}

func TestHandleLibraryBookContentStreams(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/books/1/content", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	handleLibraryBookContent(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("expected epub content-type passthrough, got %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatal("expected epub bytes")
	}
}

func TestLibraryBooksMockFallback(t *testing.T) {
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = "http://127.0.0.1:1" // unreachable
	defer func() { grimmoryBaseURL = oldURL }()
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	req := httptest.NewRequest("GET", "/api/v1/library/books", nil)
	rec := httptest.NewRecorder()
	handleLibraryBooks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mock fallback should serve 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "(Mock)") {
		t.Fatal("mock fallback books must carry the (Mock) suffix")
	}
}

func TestGetGrimmoryTokenLogsInAndCaches(t *testing.T) {
	logins := 0
	srv := fakeGrimmory(t, &logins)
	defer srv.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	defer func() { grimmoryBaseURL = oldURL }()
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()

	tok, err := getGrimmoryToken(context.Background())
	if err != nil || tok != "tok-1" {
		t.Fatalf("got %q, %v; want tok-1", tok, err)
	}
	if _, err := getGrimmoryToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins != 1 {
		t.Fatalf("expected 1 login (cached second call), got %d", logins)
	}
}

func TestGrimmoryGETAttachesBearer(t *testing.T) {
	logins := 0
	srv := fakeGrimmory(t, &logins)
	defer srv.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	defer func() { grimmoryBaseURL = oldURL }()
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()

	resp, err := grimmoryGET(context.Background(), "/api/v1/books")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestValidateProgress(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", `{"locator":{"cfi":"epubcfi(/6/4!/4)","fraction":0.42},"percent":0.42}`, true},
		{"percent out of range", `{"locator":{},"percent":1.5}`, false},
		{"negative percent", `{"locator":{},"percent":-0.1}`, false},
		{"not json", `nope`, false},
		{"oversized locator", `{"locator":{"pad":"` + strings.Repeat("x", 5000) + `"},"percent":0}`, false},
	}
	for _, c := range cases {
		_, _, err := validateProgress([]byte(c.in))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}
