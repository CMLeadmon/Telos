package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
		w.Write([]byte(`[]`))
	})
	return httptest.NewServer(mux)
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
