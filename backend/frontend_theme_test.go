//go:build embedfrontend

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stand-in for the embedded export: one HTML document and one asset.
func themeTestHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="en" data-theme="synthwave"><body>x</body></html>`))
	})
	mux.HandleFunc("/_next/static/app.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(`[data-theme="synthwave"]{--canvas:#000}`))
	})
	return themedFrontend(mux)
}

func getWithThemeCookie(t *testing.T, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: themeCookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	themeTestHandler().ServeHTTP(rec, req)
	return rec
}

// The middleware can only stamp a body it is given, so a revalidation that
// short-circuits to 304 would hand a themed client back its cached
// default-theme document. net/http answers 304 whenever the file it serves has
// a real modtime, which a directory-backed export has and the embedded one does
// not — so serve this one from disk, where the failure is reachable.
func TestThemedFrontendStampsRevalidatedDocument(t *testing.T) {
	dir := t.TempDir()
	document := `<!DOCTYPE html><html lang="en" data-theme="synthwave"><body>x</body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(document), 0o600); err != nil {
		t.Fatalf("write export document: %v", err)
	}
	handler := themedFrontend(http.FileServer(http.Dir(dir)))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: themeCookieName, Value: "ink"})
	// Anything after the file's modtime makes net/http answer 304.
	req.Header.Set("If-Modified-Since", time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a revalidated document cannot be themed", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-theme="ink"`) {
		t.Fatalf("body = %q, want the ink theme stamped", rec.Body.String())
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Cookie") {
		t.Fatalf("Vary = %q, want Cookie so a shared cache cannot cross themes", vary)
	}
}

func TestThemedFrontendStampsValidCookie(t *testing.T) {
	rec := getWithThemeCookie(t, "/chat/", "ink")
	body := rec.Body.String()
	if !strings.Contains(body, `data-theme="ink"`) {
		t.Fatalf("expected data-theme=\"ink\" in served HTML, got: %s", body)
	}
	if strings.Contains(body, `data-theme="synthwave"`) {
		t.Fatalf("default theme should have been replaced, got: %s", body)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Cookie") {
		t.Fatalf("themed HTML must Vary on Cookie so it is never cross-served, got %q", got)
	}
	// The body changed, so a validator for the original bytes must not survive.
	if rec.Header().Get("ETag") != "" {
		t.Fatalf("ETag must be dropped once the body is rewritten")
	}
}

func TestThemedFrontendDefaultsWhenNoCookie(t *testing.T) {
	rec := getWithThemeCookie(t, "/chat/", "")
	if !strings.Contains(rec.Body.String(), `data-theme="synthwave"`) {
		t.Fatalf("without a cookie the document keeps the built-in default")
	}
}

// The cookie is attacker-controllable, so anything outside the allow-list must
// fall back to the default rather than reach the document.
func TestThemedFrontendRejectsHostileCookie(t *testing.T) {
	hostile := []string{
		`ink" onload="alert(1)`,
		`"><script>alert(1)</script>`,
		"SYNTHWAVE",
		"dark",
		"",
		strings.Repeat("a", 5000),
	}
	for _, value := range hostile {
		rec := getWithThemeCookie(t, "/chat/", value)
		body := rec.Body.String()
		if !strings.Contains(body, `data-theme="synthwave"`) {
			t.Fatalf("hostile cookie %q should fall back to the default, got: %s", value, body)
		}
		if strings.Contains(body, "script") || strings.Contains(body, "onload") {
			t.Fatalf("hostile cookie %q leaked into the document: %s", value, body)
		}
	}
}

// Only HTML documents are rewritten; assets must pass through untouched, or a
// stylesheet's own [data-theme="synthwave"] selectors would be corrupted.
func TestThemedFrontendLeavesAssetsAlone(t *testing.T) {
	rec := getWithThemeCookie(t, "/_next/static/app.css", "ink")
	if got := rec.Body.String(); got != `[data-theme="synthwave"]{--canvas:#000}` {
		t.Fatalf("asset body was modified: %s", got)
	}
	if rec.Header().Get("Vary") != "" {
		t.Fatalf("assets do not vary by cookie")
	}
}
