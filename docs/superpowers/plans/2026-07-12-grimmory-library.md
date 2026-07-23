# Grimmory Library Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Library placeholder with a working e-book module: gateway endpoints translating Grimmory's API, a catalog UI with facet filtering, an in-app EPUB reader (foliate-js) with per-user progress in Postgres, and an audiobook shelf backed by Jellyfin.

**Architecture:** The Go gateway (`backend/`) gains a `library.go` file (mirroring the `settings.go` precedent) that logs into Grimmory with admin credentials to mint short-lived JWTs, translates `/api/v1/library/*` calls onto Grimmory's `/api/v1/books*` API with Redis caching and mock fallbacks, and stores reading progress per Telos user in Postgres. The frontend gains a `useLibraryStore` Zustand store, a catalog page with facet rail + cover grid, and a full-screen reader overlay. Audiobooks ride the existing Jellyfin media/stream proxy.

**Tech Stack:** Go 1.22 (stdlib + pgx + go-redis, already present), Postgres migration `0005`, Next.js 16 static export, Zustand, foliate-js (EPUB), browser-native PDF viewing, Playwright e2e.

## Global Constraints

- Go is NOT installed on the host. Run all Go commands via: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...` (from repo root). A local `go build` fails unless `backend/out/` exists — it does in this checkout; tests are fine either way.
- All credentials via `.env` interpolation — never hardcode secrets. `.env` already contains `GRIMMORY_ADMIN_USER=telos-gateway` and `GRIMMORY_ADMIN_PASSWORD=<set>` (created during planning; the Grimmory admin account exists and works).
- Copyleft boundary: integrate with Grimmory over HTTP only — never link its code into the gateway.
- Schema changes go in `backend/db/migrations/` as a new numbered file using `IF NOT EXISTS` patterns (migrations dir supersedes `schema.sql`).
- Frontend is a static export: no server components, no API routes. Dev on :3000 talks to gateway on :8080 via `apiBase()` in `frontend/src/lib/api.ts`.
- The gateway marshals empty Go slices as JSON `null` — frontend must normalize with the existing `asList` pattern.
- Next.js 16 is newer than training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work.
- The frontend lint config rejects setState-in-effect; use promise-chain loaders as in existing stores/pages.
- Commit after every task with conventional-commit messages; end each commit message with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Verified Grimmory Facts (probed live 2026-07-12 — do NOT re-derive from the spec)

Grimmory is a BookLore rebrand (JWT issuer `booklore`). Base URL inside the compose network: `http://grimmory:6060`.

- **Auth:** `POST /api/v1/auth/login` `{"username","password"}` → `{"accessToken":"<jwt>","expires":7200,"refreshToken":"<jwt>"}`. All API calls need `Authorization: Bearer <accessToken>`. **The static `GRIMMORY_API_TOKEN` is NOT accepted (401)** — the spec's static-token assumption is wrong; the existing `/grimmory/` proxy in `main.go` is currently broken because of this.
- **Setup:** already completed during planning (`POST /api/v1/setup`, admin user `telos-gateway`). A library `{"id":1,"name":"Books","paths":[{"path":"/books"}],"watch":true}` exists and has scanned one seeded EPUB (Pride and Prejudice, book id 1).
- **Books list:** `GET /api/v1/books` → JSON array; verified item shape:
  ```json
  {"addedOn":"2026-07-12T20:09:10Z","id":1,"libraryId":1,"libraryName":"Books",
   "metadata":{"bookId":1,"title":"Pride and Prejudice","publishedDate":"1998-06-01",
     "language":"en","authors":["Jane Austen"],"categories":["Love stories","..."]},
   "primaryFile":{"bookType":"EPUB","extension":"epub","fileName":"pride-and-prejudice.epub",
     "filePath":"/books/pride-and-prejudice.epub","fileSizeKb":24253}}
  ```
- **Facets:** the spec's `Grimmory GET /api/v1/books/facets` **does not exist** (500). The gateway derives facets from the books list instead. Update the spec doc (Task 8).
- **Cover image:** `GET /api/v1/media/book/{id}/thumbnail` → JPEG bytes but with a **mislabeled `application/json` content-type** — the gateway must force `image/jpeg`. (`/api/v1/books/{id}/cover` is NOT an API route — it returns the SPA HTML shell.)
- **Book file:** `GET /api/v1/books/{id}/content` → `application/epub+zip` raw bytes (also works for PDFs). `/download` also exists (`application/octet-stream`).
- **Content mounts:** host `/mnt/storage/shared/books` → container `/books` (scanned library, watch enabled); `/mnt/storage/shared/bookdrop` → `/bookdrop` (auto-import; the Files module's "bookdrop" upload destination already writes here via `POST /api/v1/files/books`). Host dirs are owned by the container-mapped UID — seed files with `podman cp <file> telos-grimmory:/books/`.

## File Structure

- Create: `backend/library.go` (Grimmory client + all library handlers), `backend/library_test.go`, `backend/db/migrations/0005_library.sql`
- Modify: `backend/main.go` (route registration ~line 231, required-env list line 256, `handleGrimmoryProxy` ~line 1785, `LibraryItem` struct ~line 1477 + `handleMedia` ~line 1536)
- Modify: `.env.example`, `docker-compose.yml` (telos-core environment: swap `GRIMMORY_API_TOKEN` for admin creds)
- Create: `frontend/src/stores/useLibraryStore.ts`, `frontend/src/components/library/BookReader.tsx`, `frontend/src/styles/library.css`, `frontend/e2e/library.spec.ts`
- Modify: `frontend/src/app/(shell)/library/page.tsx` (replace placeholder), `frontend/src/lib/api.ts` (URL helpers), `frontend/src/stores/useMediaStore.ts` (collectionType), `frontend/src/app/layout.tsx` or wherever module CSS is imported (match how `files.css` is wired — check `grep -rn "files.css" frontend/src`)
- Modify: `documentation/architecture/03-gateway-and-api.md` (translation matrix reality), `CLAUDE.md` (build-order status)

---

### Task 1: Grimmory JWT auth client + retire the dead static token

**Files:**
- Create: `backend/library.go`, `backend/library_test.go`
- Modify: `backend/main.go` (env list line 256, `handleGrimmoryProxy` lines 1782–1795), `.env.example`, `docker-compose.yml`

**Interfaces:**
- Produces: `grimmoryBaseURL string` (package var, overridable in tests), `getGrimmoryToken(ctx context.Context) (string, error)`, `grimmoryGET(ctx context.Context, path string) (*http.Response, error)` — used by every later backend task.

- [ ] **Step 1: Write the failing test**

`backend/library_test.go`:

```go
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

// fakeGrimmory returns a test server that accepts login for user/pass and
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from repo root): `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestGetGrimmoryToken -v ./...`
Expected: FAIL — `undefined: grimmoryBaseURL`, `grimmoryTok`, `getGrimmoryToken`.

- [ ] **Step 3: Implement the client in `backend/library.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Library — Grimmory client (JWT auth)
// ═══════════════════════════════════════════════════════════════════════════

// Grimmory (BookLore) does not accept static API tokens; the gateway logs in
// with admin credentials and holds a short-lived JWT (expires: 7200s).
var grimmoryBaseURL = "http://grimmory:6060"

var grimmoryTok struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func grimmoryLogin(ctx context.Context) (string, time.Duration, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": os.Getenv("GRIMMORY_ADMIN_USER"),
		"password": os.Getenv("GRIMMORY_ADMIN_PASSWORD"),
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		grimmoryBaseURL+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("grimmory login returned %s", resp.Status)
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		Expires     int    `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, err
	}
	if body.AccessToken == "" {
		return "", 0, fmt.Errorf("grimmory login returned empty token")
	}
	return body.AccessToken, time.Duration(body.Expires) * time.Second, nil
}

func getGrimmoryToken(ctx context.Context) (string, error) {
	grimmoryTok.mu.Lock()
	defer grimmoryTok.mu.Unlock()
	// 60s slack so a token never expires mid-request.
	if grimmoryTok.token != "" && time.Now().Before(grimmoryTok.expiresAt.Add(-60*time.Second)) {
		return grimmoryTok.token, nil
	}
	tok, ttl, err := grimmoryLogin(ctx)
	if err != nil {
		return "", err
	}
	grimmoryTok.token = tok
	grimmoryTok.expiresAt = time.Now().Add(ttl)
	return tok, nil
}

// grimmoryGET performs an authenticated GET, re-logging-in once on 401
// (token revoked server-side, e.g. after a Grimmory restart).
func grimmoryGET(ctx context.Context, path string) (*http.Response, error) {
	do := func(tok string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", grimmoryBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		return http.DefaultClient.Do(req)
	}
	tok, err := getGrimmoryToken(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := do(tok)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		grimmoryTok.mu.Lock()
		grimmoryTok.token = ""
		grimmoryTok.mu.Unlock()
		if tok, err = getGrimmoryToken(ctx); err != nil {
			return nil, err
		}
		return do(tok)
	}
	return resp, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run "TestGetGrimmoryToken|TestGrimmoryGET" -v ./...`
Expected: PASS (2 tests).

- [ ] **Step 5: Retire the static token in `main.go`**

In `backend/main.go` line 256, replace `"GRIMMORY_API_TOKEN",` with `"GRIMMORY_ADMIN_USER", "GRIMMORY_ADMIN_PASSWORD",` in the required-env warning list.

Replace `handleGrimmoryProxy` (lines ~1785–1795) so the director injects the JWT:

```go
func handleGrimmoryProxy(w http.ResponseWriter, r *http.Request) {
	grimmoryURL, _ := url.Parse(grimmoryBaseURL)
	proxy := httputil.NewSingleHostReverseProxy(grimmoryURL)
	tok, err := getGrimmoryToken(r.Context())
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	proxy.ServeHTTP(w, r)
}
```

Keep whatever CORS-header stripping / ModifyResponse the current implementation has (read it first; `TestProxyRequestStripsUpstreamCORSHeaders` in `main_test.go` guards this behavior — do not break it).

- [ ] **Step 6: Update `.env.example` and `docker-compose.yml`**

In `.env.example`: remove `GRIMMORY_API_TOKEN`, add `GRIMMORY_ADMIN_USER=` and `GRIMMORY_ADMIN_PASSWORD=` with a comment `# Grimmory admin account the gateway logs in with (create via first-run setup)`. In `docker-compose.yml` under `telos-core` `environment:`, replace `- GRIMMORY_API_TOKEN=${GRIMMORY_API_TOKEN}` with the two new vars. Leave the stale `GRIMMORY_API_TOKEN` line in the local `.env` alone or delete it — it is unused either way.

- [ ] **Step 7: Full test + vet, then commit**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test ./... && go vet ./..."`
Expected: all PASS (including the pre-existing proxy CORS test).

```bash
git add backend/library.go backend/library_test.go backend/main.go .env.example docker-compose.yml
git commit -m "feat(backend): Grimmory JWT auth client, retire dead static API token"
```

---

### Task 2: Migration 0005 — per-user book progress table

**Files:**
- Create: `backend/db/migrations/0005_library.sql`

**Interfaces:**
- Produces: table `book_progress(user_id UUID, book_id BIGINT, locator JSONB, percent REAL, updated_at)` — consumed by Task 4 handlers.

- [ ] **Step 1: Write the migration**

```sql
-- Migration 0005: Library — per-user reading progress for Grimmory books.
-- Progress lives in Telos (not Grimmory) because the gateway talks to
-- Grimmory as a single admin account; book_id is Grimmory's numeric book id.

CREATE TABLE IF NOT EXISTS book_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id BIGINT NOT NULL,
    locator JSONB NOT NULL DEFAULT '{}',
    percent REAL NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, book_id)
);
```

- [ ] **Step 2: Verify it applies** — the gateway applies migrations at startup; defer live verification to Task 5's rebuild (migration syntax is exercised there). Sanity-check locally: `podman exec telos-postgres psql -U $POSTGRES_USER -d $POSTGRES_DB -c '\d book_progress'` is expected to fail NOW (table absent) and succeed after Task 5.

- [ ] **Step 3: Commit**

```bash
git add backend/db/migrations/0005_library.sql
git commit -m "feat(backend): add book_progress migration"
```

---

### Task 3: Books list, derived facets, cover & content proxies

**Files:**
- Modify: `backend/library.go`, `backend/library_test.go`, `backend/main.go` (route registration after line 225)

**Interfaces:**
- Consumes: `grimmoryGET` from Task 1.
- Produces HTTP API (all behind `withAuth(..., "view_library")`):
  - `GET /api/v1/library/books` → `[]LibraryBook`
  - `GET /api/v1/library/facets` → `LibraryFacets`
  - `GET /api/v1/library/books/{id}/cover` → JPEG bytes
  - `GET /api/v1/library/books/{id}/content` → book file bytes
- Produces Go types (frontend mirrors these):

```go
type LibraryBook struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Authors    []string `json:"authors"`
	Categories []string `json:"categories"`
	Language   string   `json:"language"`
	Format     string   `json:"format"`   // primaryFile.bookType: "EPUB" | "PDF"
	FileSizeKB int64    `json:"fileSizeKb"`
	AddedOn    string   `json:"addedOn"`
	Library    string   `json:"library"`
}

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type LibraryFacets struct {
	Authors    []FacetValue `json:"authors"`
	Categories []FacetValue `json:"categories"`
	Languages  []FacetValue `json:"languages"`
	Formats    []FacetValue `json:"formats"`
}
```

- [ ] **Step 1: Write failing tests** (append to `library_test.go`; extend `fakeGrimmory` with the books/cover/content routes)

Add to `fakeGrimmory`'s mux:

```go
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
```

Change the `GET /api/v1/books` stub body from `[]` to one verified-shape book:

```go
		w.Write([]byte(`[{"addedOn":"2026-07-12T20:09:10Z","id":1,"libraryId":1,"libraryName":"Books",
			"metadata":{"bookId":1,"title":"Pride and Prejudice","language":"en",
				"authors":["Jane Austen"],"categories":["Love stories","England -- Fiction"]},
			"primaryFile":{"bookType":"EPUB","extension":"epub","fileName":"pp.epub","fileSizeKb":24253}}]`))
```

New tests:

```go
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
```

(Add `"bytes"`, `"strings"` to the test imports.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestHandleLibrary -v ./...`
Expected: FAIL — `undefined: handleLibraryBooks` etc.

- [ ] **Step 3: Implement in `library.go`**

```go
// ═══════════════════════════════════════════════════════════════════════════
// Library — books catalog (translated from Grimmory)
// ═══════════════════════════════════════════════════════════════════════════

// grimmoryBook is the verified upstream shape (BookLore GET /api/v1/books).
type grimmoryBook struct {
	ID          int64  `json:"id"`
	AddedOn     string `json:"addedOn"`
	LibraryName string `json:"libraryName"`
	Metadata    struct {
		Title      string   `json:"title"`
		Language   string   `json:"language"`
		Authors    []string `json:"authors"`
		Categories []string `json:"categories"`
	} `json:"metadata"`
	PrimaryFile struct {
		BookType   string `json:"bookType"`
		FileSizeKB int64  `json:"fileSizeKb"`
	} `json:"primaryFile"`
}

func fetchGrimmoryBooks(ctx context.Context) ([]LibraryBook, error) {
	resp, err := grimmoryGET(ctx, "/api/v1/books")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grimmory books returned %s", resp.Status)
	}
	var raw []grimmoryBook
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	books := make([]LibraryBook, 0, len(raw))
	for _, b := range raw {
		books = append(books, LibraryBook{
			ID: b.ID, Title: b.Metadata.Title, Authors: b.Metadata.Authors,
			Categories: b.Metadata.Categories, Language: b.Metadata.Language,
			Format: b.PrimaryFile.BookType, FileSizeKB: b.PrimaryFile.FileSizeKB,
			AddedOn: b.AddedOn, Library: b.LibraryName,
		})
	}
	return books, nil
}

var mockLibraryBooks = []LibraryBook{
	{ID: 1, Title: "The Sovereign Stack (Mock)", Authors: []string{"Telos Docs Team (Mock)"},
		Categories: []string{"Technology"}, Language: "en", Format: "EPUB", FileSizeKB: 1024,
		AddedOn: "2026-01-01T00:00:00Z", Library: "Books (Mock)"},
	{ID: 2, Title: "Single Origin (Mock)", Authors: []string{"Gateway Author (Mock)"},
		Categories: []string{"Fiction"}, Language: "en", Format: "PDF", FileSizeKB: 2048,
		AddedOn: "2026-01-02T00:00:00Z", Library: "Books (Mock)"},
}

// getLibraryBooks serves from Redis (60s), then Grimmory, then mocks.
func getLibraryBooks(ctx context.Context) []LibraryBook {
	const cacheKey = "telos:grimmory:books"
	if redisClient != nil {
		if val, err := redisClient.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			var books []LibraryBook
			if json.Unmarshal([]byte(val), &books) == nil {
				return books
			}
		}
	}
	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		log.Printf("WARN: grimmory books unavailable, serving mock data: %v", err)
		return mockLibraryBooks
	}
	if redisClient != nil {
		if raw, err := json.Marshal(books); err == nil {
			_ = redisClient.Set(ctx, cacheKey, string(raw), 60*time.Second).Err()
		}
	}
	return books
}

func handleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	books := getLibraryBooks(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
}

// Grimmory's /api/v1/books/facets endpoint does not exist in the deployed
// build (500s); facets are derived here from the book list instead.
func handleLibraryFacets(w http.ResponseWriter, r *http.Request) {
	books := getLibraryBooks(r.Context())
	count := func(pick func(LibraryBook) []string) []FacetValue {
		m := map[string]int{}
		for _, b := range books {
			for _, v := range pick(b) {
				if v != "" {
					m[v]++
				}
			}
		}
		out := make([]FacetValue, 0, len(m))
		for v, c := range m {
			out = append(out, FacetValue{Value: v, Count: c})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Value < out[j].Value
		})
		return out
	}
	facets := LibraryFacets{
		Authors:    count(func(b LibraryBook) []string { return b.Authors }),
		Categories: count(func(b LibraryBook) []string { return b.Categories }),
		Languages:  count(func(b LibraryBook) []string { return []string{b.Language} }),
		Formats:    count(func(b LibraryBook) []string { return []string{b.Format} }),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(facets)
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — cover & content proxies
// ═══════════════════════════════════════════════════════════════════════════

func proxyGrimmoryBinary(w http.ResponseWriter, r *http.Request, path, forceContentType string) {
	resp, err := grimmoryGET(r.Context(), path)
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Grimmory returned "+resp.Status, http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if forceContentType != "" {
		ct = forceContentType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	io.Copy(w, resp.Body)
}

func libraryBookID(r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" {
		return "", false
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", false
	}
	return id, true
}

func handleLibraryBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	// Upstream serves JPEG bytes with a JSON content-type — force the real one.
	proxyGrimmoryBinary(w, r, "/api/v1/media/book/"+id+"/thumbnail", "image/jpeg")
}

func handleLibraryBookContent(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	proxyGrimmoryBinary(w, r, "/api/v1/books/"+id+"/content", "")
}
```

Add imports as needed: `"io"`, `"log"`, `"sort"`, `"strconv"`.

- [ ] **Step 4: Register routes in `main.go`** (after the `/grimmory/` line ~225)

```go
	// Library module routes (Grimmory-backed catalog)
	mux.Handle("GET /api/v1/library/books", withAuth(http.HandlerFunc(handleLibraryBooks), "view_library"))
	mux.Handle("GET /api/v1/library/facets", withAuth(http.HandlerFunc(handleLibraryFacets), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleLibraryBookCover), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/content", withAuth(http.HandlerFunc(handleLibraryBookContent), "view_library"))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test ./... && go vet ./..."`
Expected: all PASS. Note: tests run without Redis (`redisClient == nil`) — the nil-guards make caching a no-op there, matching the existing codebase pattern.

- [ ] **Step 6: Commit**

```bash
git add backend/library.go backend/library_test.go backend/main.go
git commit -m "feat(backend): library books, derived facets, cover/content proxies"
```

---

### Task 4: Reading-progress endpoints (Postgres)

**Files:**
- Modify: `backend/library.go`, `backend/library_test.go`, `backend/main.go` (routes)

**Interfaces:**
- Consumes: `book_progress` table (Task 2), `userContextKey`/`UserContext` and `dbPool` from `main.go` (same pattern as `settings.go` handlers).
- Produces:
  - `GET /api/v1/library/books/{id}/progress` → `{"locator":{...},"percent":0.42,"updatedAt":"..."}` (zero-value object when no row)
  - `PUT /api/v1/library/books/{id}/progress` body `{"locator":{...},"percent":0.42}` → `{"status":"success"}`
  - `validateProgress(raw []byte) (locator []byte, percent float64, err error)` — pure, unit-tested.

- [ ] **Step 1: Write failing tests** (append to `library_test.go`)

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestValidateProgress -v ./...`
Expected: FAIL — `undefined: validateProgress`.

- [ ] **Step 3: Implement**

```go
// ═══════════════════════════════════════════════════════════════════════════
// Library — per-user reading progress (Telos Postgres, not Grimmory: the
// gateway is a single admin account upstream, so progress must be local)
// ═══════════════════════════════════════════════════════════════════════════

func validateProgress(raw []byte) ([]byte, float64, error) {
	if len(raw) > 4096 {
		return nil, 0, errors.New("progress payload too large")
	}
	var body struct {
		Locator json.RawMessage `json:"locator"`
		Percent float64         `json:"percent"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, errors.New("invalid JSON")
	}
	if body.Percent < 0 || body.Percent > 1 {
		return nil, 0, errors.New("percent must be between 0 and 1")
	}
	locator := []byte("{}")
	if len(body.Locator) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(body.Locator, &probe); err != nil {
			return nil, 0, errors.New("locator must be a JSON object")
		}
		if len(body.Locator) > 2048 {
			return nil, 0, errors.New("locator too large")
		}
		locator = body.Locator
	}
	return locator, body.Percent, nil
}

func handleGetBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	var locator []byte
	var percent float64
	var updated time.Time
	err := dbPool.QueryRow(r.Context(), `
		SELECT locator, percent, updated_at FROM book_progress
		WHERE user_id = $1 AND book_id = $2
	`, user.ID, id).Scan(&locator, &percent, &updated)
	w.Header().Set("Content-Type", "application/json")
	if err != nil { // no row yet — zero progress
		w.Write([]byte(`{"locator":{},"percent":0,"updatedAt":null}`))
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"locator": json.RawMessage(locator), "percent": percent,
		"updatedAt": updated.Format(time.RFC3339),
	})
}

func handlePutBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	locator, percent, err := validateProgress(raw)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := dbPool.Exec(r.Context(), `
		INSERT INTO book_progress (user_id, book_id, locator, percent, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET locator = EXCLUDED.locator, percent = EXCLUDED.percent, updated_at = NOW()
	`, user.ID, id, locator, percent); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
```

Add `"errors"` import. Register routes in `main.go` next to the other library routes:

```go
	mux.Handle("GET /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handleGetBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handlePutBookProgress), "view_library"))
```

(DB-backed paths get covered by the credentialed e2e in Task 7 — same treatment as the settings module.)

- [ ] **Step 4: Run full backend gate**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test ./... && go vet ./..."`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/library.go backend/library_test.go backend/main.go
git commit -m "feat(backend): per-user reading progress endpoints"
```

---

### Task 5: Rebuild the stack and live-smoke the new API

**Files:** none (verification checkpoint — catches migration errors, env plumbing, real Grimmory drift)

- [ ] **Step 1: Rebuild telos-core**

Run: `podman-compose up -d --build telos-core`
Then: `podman logs telos-core 2>&1 | tail -30` — expect migration `0005_library.sql` applied, no missing-env warnings for `GRIMMORY_ADMIN_USER`/`GRIMMORY_ADMIN_PASSWORD`, no fallback warnings.

- [ ] **Step 2: Smoke with a real session**

Mint a session per the established e2e-account pattern (login as an existing test user via `POST /api/v1/auth/login` with an `Origin: http://localhost:8080` header — CSRF check requires it; capture the `telos_session` cookie):

```bash
curl -s -c /tmp/claude-1000/-var-home-cleadmon-Projects-Telos/3f6444f3-eddc-4387-8983-d5209607398e/scratchpad/cj.txt \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  -d '{"username":"<e2e user>","password":"<e2e pass>"}' http://localhost:8080/api/v1/auth/login
CJ=/tmp/claude-1000/-var-home-cleadmon-Projects-Telos/3f6444f3-eddc-4387-8983-d5209607398e/scratchpad/cj.txt
curl -s -b $CJ http://localhost:8080/api/v1/library/books | head -c 400          # real P&P book, no "(Mock)"
curl -s -b $CJ http://localhost:8080/api/v1/library/facets | head -c 400          # Jane Austen facet
curl -s -b $CJ -o /dev/null -w '%{http_code} %{content_type}\n' http://localhost:8080/api/v1/library/books/1/cover      # 200 image/jpeg
curl -s -b $CJ -o /dev/null -w '%{http_code} %{content_type} %{size_download}\n' http://localhost:8080/api/v1/library/books/1/content  # 200 application/epub+zip ~24MB
curl -s -b $CJ -X PUT -H 'Content-Type: application/json' -H 'Origin: http://localhost:8080' \
  -d '{"locator":{"fraction":0.1},"percent":0.1}' http://localhost:8080/api/v1/library/books/1/progress
curl -s -b $CJ http://localhost:8080/api/v1/library/books/1/progress               # echoes 0.1
```

Expected: every line as annotated. If books come back `(Mock)`, read `podman logs telos-core` for the WARN and fix before proceeding — a working-looking response is not proof.

- [ ] **Step 3: Commit** — nothing to commit unless fixes were needed.

---

### Task 6: Frontend — API helpers, library store, catalog UI

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Create: `frontend/src/stores/useLibraryStore.ts`, `frontend/src/styles/library.css`
- Modify: `frontend/src/app/(shell)/library/page.tsx`, plus the CSS wiring point (find with `grep -rn "files.css" frontend/src` and mirror it)

**Interfaces:**
- Consumes: gateway API from Tasks 3–4; `api`/`apiBase`/`ApiError` from `lib/api.ts`.
- Produces: `useLibraryStore` with `{books, facets, status, error, filters, setFilter, clearFilters, fetchCatalog, filtered()}`; `libraryCoverUrl(id: number)`, `libraryContentUrl(id: number)`; `LibraryBook`, `LibraryFacets` TS types mirroring the Go JSON. Task 7's reader consumes `libraryContentUrl` and the progress endpoints.

- [ ] **Step 1: Add URL helpers to `frontend/src/lib/api.ts`**

```ts
export function libraryCoverUrl(bookId: number): string {
  return `${apiBase()}/api/v1/library/books/${bookId}/cover`;
}

export function libraryContentUrl(bookId: number): string {
  return `${apiBase()}/api/v1/library/books/${bookId}/content`;
}
```

- [ ] **Step 2: Create `frontend/src/stores/useLibraryStore.ts`**

```ts
import { create } from "zustand";
import { api } from "@/lib/api";

export interface LibraryBook {
  id: number;
  title: string;
  authors: string[] | null;
  categories: string[] | null;
  language: string;
  format: string; // "EPUB" | "PDF"
  fileSizeKb: number;
  addedOn: string;
  library: string;
}

export interface FacetValue {
  value: string;
  count: number;
}

export interface LibraryFacets {
  authors: FacetValue[] | null;
  categories: FacetValue[] | null;
  languages: FacetValue[] | null;
  formats: FacetValue[] | null;
}

export interface LibraryFilters {
  author: string | null;
  category: string | null;
  format: string | null;
  search: string;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
export function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

const EMPTY_FILTERS: LibraryFilters = {
  author: null,
  category: null,
  format: null,
  search: "",
};

interface LibraryState {
  books: LibraryBook[];
  facets: LibraryFacets | null;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  filters: LibraryFilters;
  fetchCatalog: () => Promise<void>;
  setFilter: (key: keyof LibraryFilters, value: string | null) => void;
  clearFilters: () => void;
  filtered: () => LibraryBook[];
}

export const useLibraryStore = create<LibraryState>()((set, get) => ({
  books: [],
  facets: null,
  status: "idle",
  error: null,
  filters: EMPTY_FILTERS,

  fetchCatalog: async () => {
    if (get().status === "loading") return;
    set({ status: "loading", error: null });
    try {
      const [books, facets] = await Promise.all([
        api<LibraryBook[] | null>("/api/v1/library/books"),
        api<LibraryFacets>("/api/v1/library/facets"),
      ]);
      set({ books: asList(books), facets, status: "ready" });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load library",
      });
    }
  },

  setFilter: (key, value) =>
    set((s) => ({ filters: { ...s.filters, [key]: value ?? (key === "search" ? "" : null) } })),

  clearFilters: () => set({ filters: EMPTY_FILTERS }),

  filtered: () => {
    const { books, filters } = get();
    const q = filters.search.trim().toLowerCase();
    return books.filter((b) => {
      if (filters.author && !asList(b.authors).includes(filters.author)) return false;
      if (filters.category && !asList(b.categories).includes(filters.category)) return false;
      if (filters.format && b.format !== filters.format) return false;
      if (q) {
        const hay = `${b.title} ${asList(b.authors).join(" ")}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  },
}));
```

- [ ] **Step 3: Build the catalog page**

Replace `frontend/src/app/(shell)/library/page.tsx`. Before writing it, read `frontend/src/app/(shell)/files/page.tsx` end to end and mirror its idioms exactly (client component marker, promise-chain loading effect to satisfy the lint rule, notice/error surfaces, `data-testid` conventions, CSS class naming). Structure:

- `"use client"` page. On mount: `useEffect(() => { void fetchCatalog(); }, [fetchCatalog])` (fetch is a promise chain inside the store — no setState-in-effect).
- Layout: `<div className="library">` with a facet rail (`<aside className="library-rail">`) listing Authors / Categories / Formats from facets (buttons toggling `setFilter`, active state class, counts shown), a header row with a search `<input data-testid="library-search">` and result count, and a cover grid `<div className="library-grid" data-testid="library-grid">`.
- Each book: `<article className="library-card" data-testid="library-card">` with `<img src={libraryCoverUrl(b.id)} alt="" loading="lazy">`, title, authors line, a format pill (`EPUB`/`PDF`), and actions: **Read** button (`aria-label={`read ${b.title}`}`) for EPUBs opening the Task 7 reader; for PDFs an `<a href={libraryContentUrl(b.id)} target="_blank" rel="noreferrer">Open</a>` (browser-native PDF viewer; the session cookie rides along same-origin).
- Progress badge: after catalog load, the page does NOT bulk-fetch progress (no bulk endpoint — YAGNI); the reader restores position itself on open.
- Empty state (`no books match` when filters active, onboarding hint when catalog empty), `status === "error"` state with the store error and a retry button. All copy lowercase-terse matching the existing modules' voice.

- [ ] **Step 4: Create `frontend/src/styles/library.css` and wire it**

Read `frontend/src/styles/files.css` first and reuse its tokens/vars. Minimum classes: `.library` (rail + main grid layout, `display:grid; grid-template-columns: 220px 1fr;` collapsing to one column under 720px), `.library-rail` (facet groups, toggle buttons with active state), `.library-grid` (`repeat(auto-fill, minmax(150px, 1fr))`), `.library-card` (cover aspect-ratio 2/3, hover lift consistent with the design system), `.library-card img` (object-fit cover, fallback background for missing covers), format pill, search input. Import it exactly where `files.css` is imported.

- [ ] **Step 5: Lint + build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: clean. Fix any setState-in-effect complaints with the promise-chain pattern.

- [ ] **Step 6: Manual smoke**

With `npm run dev` running and the gateway up: log in at `http://localhost:3000/login/`, open `/library/`, verify the Pride and Prejudice card renders with its real cover, facet buttons filter, search filters.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/stores/useLibraryStore.ts frontend/src/styles/library.css "frontend/src/app/(shell)/library/page.tsx" <css wiring file>
git commit -m "feat(frontend): library catalog with facet filtering"
```

---

### Task 7: In-app EPUB reader with progress persistence

**Files:**
- Create: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/app/(shell)/library/page.tsx` (open/close reader state), `frontend/src/styles/library.css` (reader overlay), `frontend/package.json` (dependency)

**Interfaces:**
- Consumes: `libraryContentUrl` (Task 6), `GET/PUT /api/v1/library/books/{id}/progress` (Task 4).
- Produces: `<BookReader book={LibraryBook} onClose={() => void} />` — full-screen overlay.

- [ ] **Step 1: Vet the foliate-js package before adopting it**

`npm view foliate-js` reports v1.0.1 on npm, but foliate-js upstream historically did NOT publish to npm — **verify authenticity before installing**: check `npm view foliate-js repository maintainers` and confirm it points at the real johnfactotum/foliate-js project. Then `npm install foliate-js` and read `node_modules/foliate-js/` (README + `view.js`) to confirm the API: the expected surface is an ES module that registers a `<foliate-view>` custom element with `.open(book | file | url)`, `.goLeft()`/`.goRight()`, `.goToFraction(n)`, and a `relocate` CustomEvent whose `detail` carries `{fraction, cfi, ...}`.

**Decision gate:** if the npm package is not the genuine project or its API differs materially, uninstall it and use `epubjs` (`npm install epubjs`) instead — same component contract, epub.js API (`ePub(arrayBuffer)`, `book.renderTo`, `rendition.display(cfi)`, `rendition.on("relocated", ...)`, `rendition.prev()/next()`). Record which path was taken in the commit message.

- [ ] **Step 2: Implement `BookReader.tsx`** (foliate-js path; adapt per Step 1 findings)

```tsx
"use client";

import { useEffect, useRef, useState } from "react";
import { X, ChevronLeft, ChevronRight } from "lucide-react";
import { api, libraryContentUrl } from "@/lib/api";
import type { LibraryBook } from "@/stores/useLibraryStore";

interface Progress {
  locator: { fraction?: number; cfi?: string };
  percent: number;
}

// foliate-view is a custom element; keep a loose handle type.
type FoliateView = HTMLElement & {
  open: (src: unknown) => Promise<void>;
  goLeft: () => void;
  goRight: () => void;
  goToFraction: (f: number) => Promise<void>;
};

export function BookReader({
  book,
  onClose,
}: {
  book: LibraryBook;
  onClose: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<FoliateView | null>(null);
  const [percent, setPercent] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let disposed = false;
    const host = hostRef.current;
    if (!host) return;

    // Dynamic import: foliate-js touches window at module scope, so it must
    // never be imported during the static export build.
    Promise.all([
      import("foliate-js/view.js"),
      fetch(libraryContentUrl(book.id), { credentials: "include" }).then((r) => {
        if (!r.ok) throw new Error(`content fetch failed (${r.status})`);
        return r.blob();
      }),
      api<Progress>(`/api/v1/library/books/${book.id}/progress`),
    ])
      .then(async ([, blob, progress]) => {
        if (disposed) return;
        const view = document.createElement("foliate-view") as FoliateView;
        host.replaceChildren(view);
        viewRef.current = view;
        view.addEventListener("relocate", (e) => {
          const detail = (e as CustomEvent).detail as {
            fraction?: number;
            cfi?: string;
          };
          const fraction = detail?.fraction ?? 0;
          setPercent(fraction);
          if (saveTimer.current) clearTimeout(saveTimer.current);
          saveTimer.current = setTimeout(() => {
            void api(`/api/v1/library/books/${book.id}/progress`, {
              method: "PUT",
              body: JSON.stringify({
                locator: { fraction, cfi: detail?.cfi },
                percent: fraction,
              }),
            }).catch(() => {}); // progress saving is best-effort
          }, 1000);
        });
        const file = new File([blob], `book-${book.id}.epub`, {
          type: "application/epub+zip",
        });
        await view.open(file);
        if (progress.locator?.fraction) {
          await view.goToFraction(progress.locator.fraction);
        }
      })
      .catch((err) => {
        if (!disposed)
          setError(err instanceof Error ? err.message : "failed to open book");
      });

    return () => {
      disposed = true;
      if (saveTimer.current) clearTimeout(saveTimer.current);
      host.replaceChildren();
      viewRef.current = null;
    };
  }, [book.id]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft") viewRef.current?.goLeft();
      if (e.key === "ArrowRight") viewRef.current?.goRight();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="reader-overlay" data-testid="book-reader" role="dialog" aria-label={`reading ${book.title}`}>
      <header className="reader-bar">
        <span className="reader-title">{book.title}</span>
        <span className="reader-percent" data-testid="reader-percent">
          {Math.round(percent * 100)}%
        </span>
        <button aria-label="close reader" onClick={onClose}>
          <X size={18} />
        </button>
      </header>
      {error ? (
        <p className="reader-error">{error}</p>
      ) : (
        <div className="reader-host" ref={hostRef} />
      )}
      <button className="reader-nav reader-nav-left" aria-label="previous page" onClick={() => viewRef.current?.goLeft()}>
        <ChevronLeft />
      </button>
      <button className="reader-nav reader-nav-right" aria-label="next page" onClick={() => viewRef.current?.goRight()}>
        <ChevronRight />
      </button>
    </div>
  );
}
```

Wire into the page: `const [reading, setReading] = useState<LibraryBook | null>(null);` — Read button sets it, `{reading && <BookReader book={reading} onClose={() => setReading(null)} />}`. Add reader overlay CSS (fixed inset-0, z above shell, `--surface` background, side nav buttons, top bar) to `library.css`.

If TypeScript lacks types for the package, add a `frontend/src/types/foliate-js.d.ts` with `declare module "foliate-js/view.js";`.

- [ ] **Step 3: Lint + build**

Run: `npm run lint && npm run build`
Expected: clean. **Static-export gotcha:** if the build fails on the dynamic import touching `window`/`customElements` at module scope, ensure the import stays inside `useEffect` (never top-level) — that is sufficient for `output: "export"`.

- [ ] **Step 4: Manual verification**

Dev server: open the P&P book, page forward a few times, close, reopen — position restores; check the gateway: `curl -s -b $CJ http://localhost:8080/api/v1/library/books/1/progress` shows a non-zero fraction.

- [ ] **Step 5: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/src/components/library/ "frontend/src/app/(shell)/library/page.tsx" frontend/src/styles/library.css
git commit -m "feat(frontend): in-app EPUB reader with per-user progress"
```

---

### Task 8: Audiobooks shelf via Jellyfin

**Files:**
- Modify: `backend/main.go` (`LibraryItem` struct ~1477, `handleMedia` mapping ~1536), `backend/main_test.go` or `library_test.go` (mapping test), `frontend/src/stores/useMediaStore.ts` (type), `frontend/src/app/(shell)/library/page.tsx` (shelf), `frontend/src/styles/library.css`

**Interfaces:**
- Consumes: existing `/api/v1/media`, `/api/v1/media/items?parentId=`, `/api/v1/stream/audio/{id}` (all already implemented).
- Produces: `LibraryItem.CollectionType string \`json:"collectionType"\`` so the frontend can single out `audiobooks` libraries; Library page audiobook shelf.

- [ ] **Step 1: Backend — expose `collectionType`**

Add `CollectionType string \`json:"collectionType"\`` to `LibraryItem` and populate it with the lowercased `item.CollectionType` inside `handleMedia`'s loop. Add a small test (follow `TestGetJellyfinUserIDPrefersConfiguredUser`'s httptest-server pattern at `main_test.go:277`) asserting a Jellyfin view with `CollectionType: "audiobooks"` maps to `{type: "audio", collectionType: "audiobooks"}`. **Cache note:** `handleMedia` serves a 5-minute Redis cache (`telos:jellyfin:libraries`) — after deploying, `podman exec telos-redis redis-cli del telos:jellyfin:libraries` so the new field appears immediately.

- [ ] **Step 2: Frontend shelf**

In `useMediaStore.ts` add `collectionType?: string` to `MediaLibrary`. In the library page, after the book grid, render an "audiobooks" section: promise-chain load of `useMediaStore` libraries, filter `collectionType === "audiobooks"`, list items via `fetchItems(libraryId)`, each row with a play button that sets a local `nowPlayingId` rendering `<audio controls autoPlay src={`${apiBase()}/api/v1/stream/audio/${item.id}`} data-testid="audiobook-player" />` (crossOrigin is unnecessary — cookie rides along with `credentials`; match how the Stream page builds audio URLs — read `frontend/src/app/(shell)/stream/page.tsx:32-45` first and reuse its helper if one exists). If no audiobook library exists, render nothing (no empty shell).

- [ ] **Step 3: Seed sample audiobook content (best-effort, live-env work)**

Check Jellyfin's mounts: `podman inspect telos-jellyfin --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}'`. Download 2–3 public-domain LibriVox MP3s (e.g. `https://www.archive.org/download/pride_and_prejudice_librivox/prideandprejudice_01_austen_64kb.mp3`) into the scratchpad, `podman cp` them into the media mount under an `audiobooks/` folder, then create an "Audiobooks" library via the Jellyfin API (`POST /Library/VirtualFolders?name=Audiobooks&collectionType=books&paths=<container path>&refreshLibrary=true` with the `X-Emby-Token: $JELLYFIN_ADMIN_TOKEN` header — verify the exact param shape against the running Jellyfin's `/System/Info` version docs first; jellyfin uses `collectionType=books` for audiobooks). Trigger a scan, confirm `curl -b $CJ http://localhost:8080/api/v1/media` now includes it. If Jellyfin refuses the API-created library after two attempts, document the manual step in the plan-completion notes and keep the e2e conditional.

- [ ] **Step 4: Gates**

Backend: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test ./... && go vet ./..."` — PASS.
Frontend: `npm run lint && npm run build` — clean.
Rebuild + manual check: `podman-compose up -d --build telos-core`, clear the Redis key, reload `/library/`, play an audiobook track.

- [ ] **Step 5: Commit**

```bash
git add backend/main.go backend/main_test.go frontend/src/stores/useMediaStore.ts "frontend/src/app/(shell)/library/page.tsx" frontend/src/styles/library.css
git commit -m "feat: audiobooks shelf in library via jellyfin collection types"
```

---

### Task 9: Credentialed e2e + docs + final verification

**Files:**
- Create: `frontend/e2e/library.spec.ts`
- Modify: `documentation/architecture/03-gateway-and-api.md` (Section 3 matrix + Grimmory auth note), `CLAUDE.md` (build-order phase 3 status)
- Delete: `frontend/e2e/_stream_debug_tmp.spec.ts` (leftover debug file — confirm with `git log`/content that it is the known temp artifact before deleting)

- [ ] **Step 1: Write `frontend/e2e/library.spec.ts`** (mirror `files.spec.ts`'s login helper and env-var skip)

```ts
import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with the seeded P&P book:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test library
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("library lists the catalog with real covers and facets", async ({ page }) => {
  await login(page);
  await page.goto("/library/");
  const card = page.getByTestId("library-card").filter({ hasText: "Pride and Prejudice" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  // Real cover, not a broken image (mock fallback would 503 the cover route).
  const img = card.locator("img");
  await expect
    .poll(async () => img.evaluate((el: HTMLImageElement) => el.naturalWidth))
    .toBeGreaterThan(0);
  // Facet filtering narrows and clears.
  await page.getByRole("button", { name: /Jane Austen/ }).click();
  await expect(page.getByTestId("library-card")).toHaveCount(1);
});

test("epub reader opens, paginates and persists progress", async ({ page }) => {
  await login(page);
  await page.goto("/library/");
  await page.getByLabel("read Pride and Prejudice").click();
  const reader = page.getByTestId("book-reader");
  await expect(reader).toBeVisible();
  // 24 MB EPUB: allow generous open time.
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", { timeout: 60_000 });
  const before = await page.getByTestId("reader-percent").textContent();
  await page.getByLabel("next page").click();
  await page.getByLabel("next page").click();
  await expect(page.getByTestId("reader-percent")).not.toHaveText(before!, { timeout: 15_000 });
  // Debounced save is 1s; give it breathing room, then verify restore.
  await page.waitForTimeout(2_000);
  await page.getByLabel("close reader").click();
  await page.reload();
  await page.getByLabel("read Pride and Prejudice").click();
  await expect(page.getByTestId("reader-percent")).not.toHaveText("0%", { timeout: 60_000 });
});
```

Note: the reader initially reports a nonzero fraction only after `relocate` fires — if the first assertion is flaky at position 0 on a fresh account, seed progress in the first test or assert on `reader.locator("foliate-view")` visibility instead; adjust against observed behavior, do not blindly retry.

- [ ] **Step 2: Run the suite**

With `npm run dev` running (playwright config has no webServer):
`E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test library` — PASS.
Then the full suite: `E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test` — no regressions (bootstrap/smoke/files still green). Use the established invite-flow test account; remember ClamAV must be warm for files.spec.

- [ ] **Step 3: Update docs**

`documentation/architecture/03-gateway-and-api.md` Section 3: replace the three Library rows with the implemented reality — books (translated), facets (**derived in gateway; Grimmory's facets endpoint absent in deployed build**), cover/content proxies, progress (**stored in telos-core Postgres, per Telos user; gateway authenticates to Grimmory via admin-credential JWT login, not a static token**). `CLAUDE.md`: mark build-order phase 3 as implemented (catalog + reader; audiobooks via Jellyfin) and update the "Books module UI is static mock" line.

- [ ] **Step 4: Final full gates (verification-before-completion)**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go test ./... && go vet ./..."
cd frontend && npm run lint && npm run build
podman-compose up -d --build telos-core && curl -s http://localhost:8080/api/v1/health
```
All green, then re-run the library e2e once against the rebuilt gateway.

- [ ] **Step 5: Commit**

```bash
git add frontend/e2e/library.spec.ts documentation/architecture/03-gateway-and-api.md CLAUDE.md
git rm frontend/e2e/_stream_debug_tmp.spec.ts
git commit -m "test(frontend): credentialed library e2e; docs: library integration reality"
```

---

## Out of Scope (explicitly)

- Collaborative annotations (placeholder copy mentions them — future task; remove that copy line as part of Task 6's page rewrite)
- Per-user Grimmory accounts / OIDC federation (spec Section 4 — deliberately deferred; static-admin + Telos-side progress chosen instead)
- Book upload UI inside the Library module (the Files module's bookdrop destination already covers ingestion)
- Grimmory metadata editing, series, physical books

## Pre-Mortem Guards

1. **Grimmory API drift:** every upstream shape in this plan was probed live on 2026-07-12 against the running container — trust these over the spec doc. If a probe-verified call fails during execution, re-probe with `podman run --rm --network telos-backend docker.io/curlimages/curl ...` before changing code.
2. **foliate-js npm authenticity:** Task 7 Step 1 is a hard gate — do not skip the repository/maintainer check; fall back to epubjs rather than shipping an unvetted package.
3. **Mock-fallback false confidence:** any "working" UI must be cross-checked against `podman logs telos-core` for WARN fallback lines (Task 5 Step 2, Task 9).
4. **Static-export breakage:** reader library must only load inside `useEffect`; `npm run build` is the gate after every frontend task.
