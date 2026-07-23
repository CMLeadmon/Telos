# Settings Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A full-stack `/settings` page for Telos covering Account & Profile (display name, avatar, password change, active sessions), Appearance (server-persisted per-user preferences), and Admin (invites with roles, user management, disable/anonymize-delete, role & permission editor).

**Architecture:** New Go handlers live in a new `backend/settings.go` (same `package main` as the single-file gateway; `main.go` only gains route registrations and small extensions to existing types/queries). Schema changes ship as migration `backend/db/migrations/0004_settings.sql` (the migration runner applies numbered files idempotently). The frontend adds a `(shell)/settings` route with a client-side section switcher, section components under `components/settings/`, a `usePreferencesStore` that syncs appearance prefs to the server, and a `settings.css` stylesheet following the existing foundations/token system.

**Tech Stack:** Go 1.22 (net/http ServeMux with method patterns, pgx v5, go-redis), Postgres, ClamAV scan pipeline, Next.js 16 static export, Zustand, lucide-react, hand-rolled CSS on Telos Design System tokens.

## Global Constraints

- **DO NOT COMMIT.** The user works on branch `docs-rewrite` with unrelated uncommitted changes and decides commits themselves. Every "commit" checkpoint below is a **verification gate only**.
- Go is not installed on the host. Backend gate (run from repo root):
  `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./... && podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go vet ./...`
  DB-dependent tests must `t.Skip` when `DATABASE_URL` is unset (existing pattern, `main_test.go:163`).
- Frontend gate (from `frontend/`): `npm run lint && npm run build`.
- Never hardcode secrets; all config from env.
- Copyleft boundary: no Jellyfin/Grimmory code in the gateway (not touched by this plan).
- API JSON style: existing auth endpoints return `UserContext` with **capitalized** field names (`ID`, `Username`, `Roles` — no json tags); new response types use explicit lowercase json tags (matches newer handlers like `ChannelResponse`). Extend `UserContext` without adding tags so `/auth/me` stays backward compatible.
- Session cookie is `telos_session`; token hash = hex(sha256(token)); permission gating via `withAuth(handler, "perm")`.
- Guardrails (business rules locked during interview):
  - Built-in `Owner` role: permissions not editable, role not deletable.
  - Only an Owner may grant or revoke the `Owner` role; the last Owner can never be demoted, disabled, or deleted.
  - Users cannot change their own roles, disable themselves, or delete themselves.
  - Hard-deleting a user = **anonymization** (messages have `ON DELETE CASCADE`, so a real `DELETE` would destroy history): username → `deleted-<first 8 of uuid>`, display name → `Deleted User`, `active=FALSE`, `password_hash='!'`, roles removed, sessions revoked, avatar cleared, preferences deleted.
  - Password change requires the current password and revokes all *other* sessions.
- Passwords: 15–128 chars (matches `handleAcceptInvite`). Display names: trimmed, ≤64 runes, no control chars, empty = clear (fall back to username).

---

### Task 1: Migration 0004 — settings schema

**Files:**
- Create: `backend/db/migrations/0004_settings.sql`
- Modify: `backend/main.go` (`UserContext` type ~line 88, `getAuthenticatedUser` query ~line 545, `handleMe` ~line 885)

**Interfaces:**
- Produces: `users.display_name VARCHAR(64) NULL`, `users.avatar_file_id UUID NULL`, `sessions.user_agent/client_ip/last_seen_at`, `invites.role_id`, `roles.builtin`, table `user_preferences(user_id, prefs JSONB, updated_at)`.
- Produces: `UserContext{ID, Username, Roles []string, DisplayName string, HasAvatar bool}` — `DisplayName` is `""` when unset; `HasAvatar` true when `avatar_file_id` is non-null. Later tasks and the frontend rely on exactly these field names (marshaled capitalized, no json tags).

- [ ] **Step 1: Write the migration**

```sql
-- Migration 0004: Settings — profiles, preferences, session metadata, invite roles, role editor

ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_file_id UUID REFERENCES files(id) ON DELETE SET NULL;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS user_agent VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS client_ip VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE invites ADD COLUMN IF NOT EXISTS role_id VARCHAR(50) REFERENCES roles(id) ON DELETE SET NULL;

ALTER TABLE roles ADD COLUMN IF NOT EXISTS builtin BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE roles SET builtin = TRUE
WHERE id IN ('Owner', 'Administrator', 'Moderator', 'Member', 'Contributor', 'Librarian');

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 2: Extend `UserContext` and `getAuthenticatedUser`**

In `main.go`, change the type (keep no json tags):

```go
type UserContext struct {
	ID          string
	Username    string
	Roles       []string
	DisplayName string
	HasAvatar   bool
}
```

In `getAuthenticatedUser`, change the session query and scan:

```go
	var displayName *string
	var avatarFileID *string

	err = dbPool.QueryRow(r.Context(), `
		SELECT s.user_id, u.username, u.active, s.expires_at, u.display_name, u.avatar_file_id
		FROM sessions s
		JOIN users u ON s.user_id = u.id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL
	`, tokenHash).Scan(&userID, &username, &active, &expiresAt, &displayName, &avatarFileID)
```

and in the returned struct:

```go
	uc := &UserContext{ID: userID, Username: username, Roles: roles}
	if displayName != nil {
		uc.DisplayName = *displayName
	}
	uc.HasAvatar = avatarFileID != nil
	return uc, nil
```

`handleMe` needs no change (it already encodes `UserContext`, which now carries the new fields).

- [ ] **Step 3: Run the backend gate**

Run (repo root): `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...`
Expected: PASS (existing tests unaffected; DB tests skip without `DATABASE_URL`). Then `go vet ./...` the same way: no findings.

- [ ] **Step 4: Checkpoint (no commit — verify migration applies)**

If the compose stack is up (`podman ps` shows `telos-core`), restart it (`podman-compose up -d --build telos-core` or ask the user) and check `podman logs telos-core` for the migration applying without error. If the stack is not running, defer to the final smoke task.

---

### Task 2: Pure validation & guardrail helpers (TDD)

**Files:**
- Create: `backend/settings.go`
- Create: `backend/settings_test.go`

**Interfaces:**
- Produces (all in `package main`, used by every later backend task):
  - `validateDisplayName(s string) (string, error)` — trims; returns `""` for empty (meaning clear); rejects >64 runes or control chars.
  - `validateNewPassword(s string) error` — 15–128 chars.
  - `validatePreferences(raw []byte) (map[string]any, error)` — ≤2048 bytes; only keys `theme` (`"synthwave"`/`"ink"`), `sceneEnabled` (bool), `reducedMotion` (bool); unknown keys rejected.
  - `slugifyRoleID(name string) (string, error)` — lowercases, spaces→`-`, keeps `[a-z0-9-]`, 2–50 chars result; error if empty after cleaning.
  - `roleChangeError(actorID string, actorIsOwner bool, targetID string, targetIsOwner bool, newRoles []string) error` — self-change forbidden; Owner grant/revoke requires `actorIsOwner`.
  - `containsRole(roles []string, role string) bool`.

- [ ] **Step 1: Write the failing tests** (`backend/settings_test.go`)

```go
package main

import (
	"strings"
	"testing"
)

func TestValidateDisplayName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"  Carter  ", "Carter", false},
		{"", "", false},
		{"   ", "", false},
		{strings.Repeat("a", 65), "", true},
		{"bad\x00name", "", true},
		{"ok name 123", "ok name 123", false},
	}
	for _, c := range cases {
		got, err := validateDisplayName(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("validateDisplayName(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Errorf("validateDisplayName(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateNewPassword(t *testing.T) {
	if err := validateNewPassword(strings.Repeat("x", 14)); err == nil {
		t.Error("14 chars should fail")
	}
	if err := validateNewPassword(strings.Repeat("x", 15)); err != nil {
		t.Errorf("15 chars should pass: %v", err)
	}
	if err := validateNewPassword(strings.Repeat("x", 129)); err == nil {
		t.Error("129 chars should fail")
	}
}

func TestValidatePreferences(t *testing.T) {
	good := []byte(`{"theme":"ink","sceneEnabled":false,"reducedMotion":true}`)
	prefs, err := validatePreferences(good)
	if err != nil {
		t.Fatalf("valid prefs rejected: %v", err)
	}
	if prefs["theme"] != "ink" {
		t.Errorf("theme = %v, want ink", prefs["theme"])
	}
	bads := [][]byte{
		[]byte(`{"theme":"neon"}`),                 // bad enum
		[]byte(`{"sceneEnabled":"yes"}`),           // bad type
		[]byte(`{"evil":true}`),                    // unknown key
		[]byte(`{`),                                // bad json
		[]byte(`{"theme":"` + strings.Repeat("a", 3000) + `"}`), // too big
	}
	for i, b := range bads {
		if _, err := validatePreferences(b); err == nil {
			t.Errorf("case %d: invalid prefs accepted", i)
		}
	}
}

func TestSlugifyRoleID(t *testing.T) {
	got, err := slugifyRoleID("  Book Club Mods! ")
	if err != nil || got != "book-club-mods" {
		t.Errorf("got %q err %v", got, err)
	}
	if _, err := slugifyRoleID("!!!"); err == nil {
		t.Error("unsluggable name should fail")
	}
	if _, err := slugifyRoleID("a"); err == nil {
		t.Error("1-char slug should fail")
	}
}

func TestRoleChangeError(t *testing.T) {
	if err := roleChangeError("u1", true, "u1", false, []string{"Member"}); err == nil {
		t.Error("self role change should fail")
	}
	if err := roleChangeError("u1", false, "u2", false, []string{"Owner"}); err == nil {
		t.Error("non-owner granting Owner should fail")
	}
	if err := roleChangeError("u1", false, "u2", true, []string{"Member"}); err == nil {
		t.Error("non-owner revoking Owner should fail")
	}
	if err := roleChangeError("u1", true, "u2", false, []string{"Owner"}); err != nil {
		t.Errorf("owner granting Owner should pass: %v", err)
	}
	if err := roleChangeError("u1", false, "u2", false, []string{"Moderator"}); err != nil {
		t.Errorf("plain change should pass: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run backend gate test command. Expected: FAIL — `undefined: validateDisplayName` etc.

- [ ] **Step 3: Implement the helpers** (`backend/settings.go`)

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ═══════════════════════════════════════════════════════════════════════════
// Settings — validation & guardrail helpers
// ═══════════════════════════════════════════════════════════════════════════

func validateDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > 64 {
		return "", errors.New("display name must be at most 64 characters")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", errors.New("display name contains invalid characters")
		}
	}
	return s, nil
}

func validateNewPassword(s string) error {
	if len(s) < 15 || len(s) > 128 {
		return errors.New("password must be 15-128 characters")
	}
	return nil
}

var validThemes = map[string]bool{"synthwave": true, "ink": true}

func validatePreferences(raw []byte) (map[string]any, error) {
	if len(raw) > 2048 {
		return nil, errors.New("preferences payload too large")
	}
	var prefs map[string]any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return nil, errors.New("invalid JSON")
	}
	for k, v := range prefs {
		switch k {
		case "theme":
			s, ok := v.(string)
			if !ok || !validThemes[s] {
				return nil, errors.New("invalid theme")
			}
		case "sceneEnabled", "reducedMotion":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
		default:
			return nil, fmt.Errorf("unknown preference key %q", k)
		}
	}
	return prefs, nil
}

func slugifyRoleID(name string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if len(slug) < 2 || len(slug) > 50 {
		return "", errors.New("role name must produce a 2-50 character identifier")
	}
	return slug, nil
}

func containsRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

func roleChangeError(actorID string, actorIsOwner bool, targetID string, targetIsOwner bool, newRoles []string) error {
	if actorID == targetID {
		return errors.New("you cannot change your own roles")
	}
	grantsOwner := containsRole(newRoles, "Owner")
	if (grantsOwner != targetIsOwner) && !actorIsOwner {
		return errors.New("only an Owner can grant or revoke the Owner role")
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run backend gate. Expected: PASS, vet clean.

---

### Task 3: Profile & preferences endpoints

**Files:**
- Modify: `backend/settings.go` (append handlers)
- Modify: `backend/main.go` (route registration block after line 170; `handleLogin` session INSERT ~line 828)
- Modify: `backend/settings_test.go` (append)

**Interfaces:**
- Consumes: Task 2 validators; `userContextKey`, `withAuth`, `dbPool`.
- Produces routes:
  - `PATCH /api/v1/users/me` — body `{"displayName": string}` → 200 `{"status":"success"}`. Empty string clears (NULL).
  - `GET /api/v1/users/me/preferences` → 200 prefs object (`{}` when none).
  - `PUT /api/v1/users/me/preferences` — full prefs object → 200 echo of stored prefs.
- Produces: `handleLogin` records `user_agent` + `client_ip` on the session row.

- [ ] **Step 1: Write failing DB-gated test** (append to `settings_test.go`; follows the skip pattern of `main_test.go:163`)

```go
func TestPreferencesRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() { dbPool = oldPool; pool.Close() }()

	var userID string
	err = dbPool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ('prefs-test-user', '!')
		ON CONFLICT (username) DO UPDATE SET username = EXCLUDED.username
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer dbPool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	if err := upsertPreferences(ctx, userID, map[string]any{"theme": "ink"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	prefs, err := loadPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if prefs["theme"] != "ink" {
		t.Errorf("theme = %v, want ink", prefs["theme"])
	}
}
```

Add `"os"`, `"context"`, and pgxpool imports if not present (they exist in `main_test.go`, but `settings_test.go` needs its own).

- [ ] **Step 2: Run to verify failure** — compile error `undefined: upsertPreferences`.

- [ ] **Step 3: Implement** (append to `settings.go`; add imports `context`, `net`, `net/http`, `time` as needed)

```go
func loadPreferences(ctx context.Context, userID string) (map[string]any, error) {
	var raw []byte
	err := dbPool.QueryRow(ctx, `
		SELECT prefs FROM user_preferences WHERE user_id = $1
	`, userID).Scan(&raw)
	if err != nil {
		return map[string]any{}, nil // no row yet — empty prefs
	}
	var prefs map[string]any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return map[string]any{}, nil
	}
	return prefs, nil
}

func upsertPreferences(ctx context.Context, userID string, prefs map[string]any) error {
	raw, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = dbPool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, prefs, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET prefs = EXCLUDED.prefs, updated_at = NOW()
	`, userID, raw)
	return err
}

func handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name, err := validateDisplayName(body.DisplayName)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var val any
	if name != "" {
		val = name
	} // nil clears the column
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET display_name = $1 WHERE id = $2
	`, val, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	prefs, err := loadPreferences(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prefs)
}

func handlePutPreferences(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	prefs, err := validatePreferences(raw)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := upsertPreferences(r.Context(), user.ID, prefs); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prefs)
}
```

Register in `main.go` (after the existing authenticated routes, ~line 171):

```go
	// Settings — profile & preferences
	mux.Handle("PATCH /api/v1/users/me", withAuth(http.HandlerFunc(handleUpdateProfile), ""))
	mux.Handle("GET /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handleGetPreferences), ""))
	mux.Handle("PUT /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handlePutPreferences), ""))
```

Add `PATCH` to the CORS allowed methods list in `corsMiddleware` (`"GET, POST, PUT, DELETE, OPTIONS, HEAD"` → `"GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"`).

In `handleLogin`, capture session metadata — replace the session INSERT with:

```go
	ua := r.Header.Get("User-Agent")
	if len(ua) > 255 {
		ua = ua[:255]
	}
	_, err = dbPool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, client_ip)
		VALUES ($1, $2, $3, $4, $5)
	`, tokenHash, userID, expiry, ua, clientIP)
```

(`clientIP` already exists in `handleLogin` scope.)

- [ ] **Step 4: Run backend gate** — PASS (DB test skips without `DATABASE_URL`), vet clean.

---

### Task 4: Password change & session management

**Files:**
- Modify: `backend/settings.go`, `backend/main.go` (routes), `backend/settings_test.go`

**Interfaces:**
- Consumes: `verifyPassword`, `hashPassword`, `validateNewPassword`, cookie/token-hash pattern from `handleLogout`.
- Produces routes:
  - `POST /api/v1/users/me/password` — `{"currentPassword","newPassword"}` → 200; 401 on wrong current password; revokes all *other* sessions.
  - `GET /api/v1/users/me/sessions` → `[{"id","createdAt","lastSeenAt","userAgent","clientIp","current"}]` (active only; `id` is the token hash — irreversible, safe to expose).
  - `DELETE /api/v1/users/me/sessions/{id}` → 200 (revokes own session by hash; revoking the current one logs you out).
  - `POST /api/v1/users/me/sessions/revoke-others` → 200 `{"revoked": n}`.
- Produces helper `currentTokenHash(r *http.Request) string` (empty when no cookie).

- [ ] **Step 1: Failing test** (append; pure helper test plus DB-gated flow)

```go
func TestCurrentTokenHash(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if h := currentTokenHash(r); h != "" {
		t.Errorf("no cookie should yield empty hash, got %q", h)
	}
	r.AddCookie(&http.Cookie{Name: "telos_session", Value: "abc"})
	want := sha256.Sum256([]byte("abc"))
	if h := currentTokenHash(r); h != hex.EncodeToString(want[:]) {
		t.Errorf("hash mismatch: %s", h)
	}
}
```

(imports: `net/http/httptest`, `crypto/sha256`, `encoding/hex`.)

- [ ] **Step 2: Run to verify failure** — `undefined: currentTokenHash`.

- [ ] **Step 3: Implement**

```go
func currentTokenHash(r *http.Request) string {
	cookie, err := r.Cookie("telos_session")
	if err != nil || cookie.Value == "" {
		return ""
	}
	h := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(h[:])
}

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := validateNewPassword(body.NewPassword); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	var hash string
	if err := dbPool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&hash); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	ok, err := verifyPassword(body.CurrentPassword, hash)
	if err != nil || !ok {
		http.Error(w, "Unauthorized: Current password is incorrect", http.StatusUnauthorized)
		return
	}
	newHash, err := hashPassword(body.NewPassword)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if _, err := dbPool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, newHash, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// Revoke every other session — a changed password invalidates old devices.
	dbPool.Exec(ctx, `
		UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND token_hash <> $2
	`, user.ID, currentTokenHash(r))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type SessionResponse struct {
	ID         string  `json:"id"`
	CreatedAt  string  `json:"createdAt"`
	LastSeenAt *string `json:"lastSeenAt"`
	UserAgent  string  `json:"userAgent"`
	ClientIP   string  `json:"clientIp"`
	Current    bool    `json:"current"`
}

func handleListMySessions(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	cur := currentTokenHash(r)
	rows, err := dbPool.Query(r.Context(), `
		SELECT token_hash, created_at, last_seen_at, user_agent, client_ip
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	sessions := []SessionResponse{}
	for rows.Next() {
		var s SessionResponse
		var created time.Time
		var lastSeen *time.Time
		if err := rows.Scan(&s.ID, &created, &lastSeen, &s.UserAgent, &s.ClientIP); err != nil {
			continue
		}
		s.CreatedAt = created.Format(time.RFC3339)
		if lastSeen != nil {
			v := lastSeen.Format(time.RFC3339)
			s.LastSeenAt = &v
		}
		s.Current = s.ID == cur
		sessions = append(sessions, s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id := r.PathValue("id")
	tag, err := dbPool.Exec(r.Context(), `
		UPDATE sessions SET revoked_at = NOW()
		WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, user.ID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	tag, err := dbPool.Exec(r.Context(), `
		UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND token_hash <> $2
	`, user.ID, currentTokenHash(r))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"revoked": tag.RowsAffected()})
}
```

Also add best-effort last-seen touching in `getAuthenticatedUser` (after a successful lookup, before return):

```go
	// Best-effort, throttled last-seen stamp; never blocks the request.
	go func(hash string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		dbPool.Exec(ctx, `
			UPDATE sessions SET last_seen_at = NOW()
			WHERE token_hash = $1 AND (last_seen_at IS NULL OR last_seen_at < NOW() - INTERVAL '60 seconds')
		`, hash)
	}(tokenHash)
```

Routes in `main.go`:

```go
	mux.Handle("POST /api/v1/users/me/password", withAuth(http.HandlerFunc(handleChangePassword), ""))
	mux.Handle("GET /api/v1/users/me/sessions", withAuth(http.HandlerFunc(handleListMySessions), ""))
	mux.Handle("DELETE /api/v1/users/me/sessions/{id}", withAuth(http.HandlerFunc(handleRevokeSession), ""))
	mux.Handle("POST /api/v1/users/me/sessions/revoke-others", withAuth(http.HandlerFunc(handleRevokeOtherSessions), ""))
```

**Note:** `withAuth` treats a 36-char `{id}` path value as a channel ID for permission checks — that only happens when `requiredPerm != ""`, and these routes pass `""`, so session token hashes (64 chars) are safe. Do not add a permission string to these routes.

- [ ] **Step 4: Run backend gate** — PASS + vet clean.

---

### Task 5: Avatar upload & serving

**Files:**
- Modify: `backend/main.go` (`processUpload` ~line 1894, routes)
- Modify: `backend/settings.go`, `backend/settings_test.go`

**Interfaces:**
- Consumes: existing ClamAV pipeline (`scanFileWithClamAV`), `files` table.
- Produces:
  - `processUpload(w, r, uploaderID string, isBook bool)` gains a generalized core: `processUploadWithLimits(w, r, uploaderID string, isBook bool, maxBytes int64, extWhitelist map[string]bool) (fileID string, ok bool)`. Existing `processUpload` becomes a thin wrapper that writes the existing JSON response, preserving current behavior exactly.
  - `POST /api/v1/users/me/avatar` — multipart `file`, images only (`.jpg .jpeg .png .webp`), 5 MiB cap, ClamAV-scanned, stored through the normal files pipeline; sets `users.avatar_file_id`. Returns `{"status":"success","avatarUrl":"/api/v1/users/<id>/avatar"}`.
  - `DELETE /api/v1/users/me/avatar` — clears `avatar_file_id`.
  - `GET /api/v1/users/{id}/avatar` — auth required (any logged-in user), serves the image bytes with the stored MIME type; 404 when unset.
- Produces: avatar URL convention `/api/v1/users/{userID}/avatar` — the frontend and chat plumbing (Task 8) build URLs exactly this way.

- [ ] **Step 1: Failing test** — extension policy is pure; test the whitelist helper:

```go
func TestAvatarExtWhitelist(t *testing.T) {
	for ext, want := range map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
		".pdf": false, ".mp4": false, ".epub": false,
	} {
		if avatarExts[ext] != want {
			t.Errorf("avatarExts[%q] = %v, want %v", ext, avatarExts[ext], want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** — `undefined: avatarExts`.

- [ ] **Step 3: Implement**

Refactor `processUpload` in `main.go`: rename the existing body to `processUploadWithLimits` with signature above; where it currently hardcodes `104857600` use `maxBytes`, where it checks `allowedExts` use `extWhitelist` (default wired by the wrapper to the current map), and instead of writing the 201 JSON at the end, `return fileID, true` (on every error path, after writing the error response, `return "", false`). Then:

```go
func processUpload(w http.ResponseWriter, r *http.Request, uploaderID string, isBook bool) {
	fileID, ok := processUploadWithLimits(w, r, uploaderID, isBook, 104857600, defaultUploadExts)
	if !ok {
		return
	}
	// (fetch filename/hash for the response exactly as before, or capture them
	//  via the return path — keep the existing response shape:
	//  {"id","filename","sha256","scan_status":"clean"})
}
```

Simplest faithful refactor: have `processUploadWithLimits` also take `writeResponse bool`; when true it writes the existing 201 JSON itself (wrapper passes true, avatar handler passes false). Choose whichever keeps `handleUploadFile`/`handleUploadBook` byte-identical in behavior.

In `settings.go`:

```go
var avatarExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

func handleUploadAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	fileID, ok := processUploadWithLimits(w, r, user.ID, false, 5*1024*1024, avatarExts, false)
	if !ok {
		return // error response already written
	}
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET avatar_file_id = $1 WHERE id = $2
	`, fileID, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "success",
		"avatarUrl": "/api/v1/users/" + user.ID + "/avatar",
	})
}

func handleDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET avatar_file_id = NULL WHERE id = $1
	`, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGetAvatar(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	var storageKey, mimeType string
	err := dbPool.QueryRow(r.Context(), `
		SELECT f.storage_key, f.mime_type
		FROM users u JOIN files f ON u.avatar_file_id = f.id
		WHERE u.id = $1 AND f.scan_status = 'clean'
	`, userID).Scan(&storageKey, &mimeType)
	if err != nil {
		http.Error(w, "No avatar", http.StatusNotFound)
		return
	}
	filePath := filepath.Join("/data/shared/staging/library", storageKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "No avatar", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, filePath)
}
```

Routes:

```go
	mux.Handle("POST /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleUploadAvatar), ""))
	mux.Handle("DELETE /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleDeleteAvatar), ""))
	mux.Handle("GET /api/v1/users/{id}/avatar", withAuth(http.HandlerFunc(handleGetAvatar), ""))
```

**Route-order caveat:** `GET /api/v1/users/{id}/avatar` and the `/users/me/...` literals — Go 1.22 ServeMux prefers the more specific literal pattern, so `me` routes win. No conflict.

- [ ] **Step 4: Run backend gate** — PASS + vet clean. Verify `handleUploadFile` behavior unchanged by reading the refactor diff carefully (same limits, same exts, same response fields).

---

### Task 6: Admin — users list, roles, disable, anonymize-delete

**Files:**
- Modify: `backend/settings.go`, `backend/main.go` (routes), `backend/settings_test.go`

**Interfaces:**
- Consumes: `roleChangeError`, `containsRole` (Task 2).
- Produces routes (perms per migration 0001 — `manage_members` for user state, `manage_roles` for role assignment):
  - `GET /api/v1/admin/users` (`manage_members`) → `[{"id","username","displayName","active","createdAt","roles":[...],"hasAvatar"}]`
  - `PUT /api/v1/admin/users/{id}/roles` (`manage_roles`) — `{"roles":["Member",...]}`
  - `POST /api/v1/admin/users/{id}/active` (`manage_members`) — `{"active":bool}`; disabling revokes sessions.
  - `DELETE /api/v1/admin/users/{id}` (`manage_members`) — anonymize.
- Produces SQL helper `ownerCount(ctx) (int, error)` and `userIsOwner(ctx, userID) (bool, error)`.

**withAuth caveat (applies to all `{id}` admin routes):** `withAuth` treats a 36-char `{id}` path value as a *channel* ID when checking permissions. For `manage_members`/`manage_roles` the channel-override switch only handles `view_channel`/`send_messages`/`join_voice` (flag stays 0), so the check falls through to the global grant — safe. Do not reuse those three channel perms on `{id}` admin routes.

- [ ] **Step 1: Failing test** — DB-gated last-owner protection:

```go
func TestAnonymizeUserPreservesRow(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() { dbPool = oldPool; pool.Close() }()

	var userID string
	err = dbPool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ('anonymize-test-user', '!')
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer dbPool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	if err := anonymizeUser(ctx, userID); err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	var username string
	var active bool
	if err := dbPool.QueryRow(ctx,
		`SELECT username, active FROM users WHERE id = $1`, userID).Scan(&username, &active); err != nil {
		t.Fatalf("row gone after anonymize: %v", err)
	}
	if active || !strings.HasPrefix(username, "deleted-") {
		t.Errorf("got username=%q active=%v", username, active)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `undefined: anonymizeUser`.

- [ ] **Step 3: Implement**

```go
func ownerCount(ctx context.Context) (int, error) {
	var n int
	err := dbPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_roles WHERE role_id = 'Owner'`).Scan(&n)
	return n, err
}

func userIsOwner(ctx context.Context, userID string) (bool, error) {
	var is bool
	err := dbPool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = $1 AND role_id = 'Owner')
	`, userID).Scan(&is)
	return is, err
}

func anonymizeUser(ctx context.Context, userID string) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	suffix := userID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET username = 'deleted-' || $2, display_name = 'Deleted User',
			password_hash = '!', active = FALSE, avatar_file_id = NULL
		WHERE id = $1
	`, userID, suffix); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_preferences WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AdminUserResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Active      bool     `json:"active"`
	CreatedAt   string   `json:"createdAt"`
	Roles       []string `json:"roles"`
	HasAvatar   bool     `json:"hasAvatar"`
}

func handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT u.id, u.username, COALESCE(u.display_name, ''), u.active, u.created_at,
			COALESCE(array_agg(ur.role_id) FILTER (WHERE ur.role_id IS NOT NULL), '{}'),
			u.avatar_file_id IS NOT NULL
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at ASC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	users := []AdminUserResponse{}
	for rows.Next() {
		var u AdminUserResponse
		var created time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Active, &created, &u.Roles, &u.HasAvatar); err != nil {
			continue
		}
		u.CreatedAt = created.Format(time.RFC3339)
		users = append(users, u)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func handleAdminSetUserRoles(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	var body struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	targetOwner, err := userIsOwner(ctx, targetID)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err := roleChangeError(actor.ID, containsRole(actor.Roles, "Owner"), targetID, targetOwner, body.Roles); err != nil {
		http.Error(w, "Forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	// Last-Owner protection: demoting the only Owner bricks the instance.
	if targetOwner && !containsRole(body.Roles, "Owner") {
		if n, err := ownerCount(ctx); err != nil || n <= 1 {
			http.Error(w, "Forbidden: cannot remove the last Owner", http.StatusForbidden)
			return
		}
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, targetID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, role := range body.Roles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, targetID, role); err != nil {
			http.Error(w, "Bad Request: unknown role "+role, http.StatusBadRequest)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminSetUserActive(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		http.Error(w, "Forbidden: you cannot disable your own account", http.StatusForbidden)
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if !body.Active {
		if isOwner, _ := userIsOwner(ctx, targetID); isOwner {
			if !containsRole(actor.Roles, "Owner") {
				http.Error(w, "Forbidden: only an Owner can disable an Owner", http.StatusForbidden)
				return
			}
			if n, _ := ownerCount(ctx); n <= 1 {
				http.Error(w, "Forbidden: cannot disable the last Owner", http.StatusForbidden)
				return
			}
		}
	}
	tag, err := dbPool.Exec(ctx, `UPDATE users SET active = $1 WHERE id = $2`, body.Active, targetID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if !body.Active {
		dbPool.Exec(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, targetID)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		http.Error(w, "Forbidden: you cannot delete your own account", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if isOwner, _ := userIsOwner(ctx, targetID); isOwner {
		if !containsRole(actor.Roles, "Owner") {
			http.Error(w, "Forbidden: only an Owner can delete an Owner", http.StatusForbidden)
			return
		}
		if n, _ := ownerCount(ctx); n <= 1 {
			http.Error(w, "Forbidden: cannot delete the last Owner", http.StatusForbidden)
			return
		}
	}
	if err := anonymizeUser(ctx, targetID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
```

Routes:

```go
	// Settings — admin: members
	mux.Handle("GET /api/v1/admin/users", withAuth(http.HandlerFunc(handleAdminListUsers), "manage_members"))
	mux.Handle("PUT /api/v1/admin/users/{id}/roles", withAuth(http.HandlerFunc(handleAdminSetUserRoles), "manage_roles"))
	mux.Handle("POST /api/v1/admin/users/{id}/active", withAuth(http.HandlerFunc(handleAdminSetUserActive), "manage_members"))
	mux.Handle("DELETE /api/v1/admin/users/{id}", withAuth(http.HandlerFunc(handleAdminDeleteUser), "manage_members"))
```

- [ ] **Step 4: Run backend gate** — PASS + vet clean.

---

### Task 7: Admin — invites with roles

**Files:**
- Modify: `backend/settings.go`, `backend/main.go` (`handleCreateInvite` ~line 891, `handleAcceptInvite` role assignment ~line 979, routes)

**Interfaces:**
- Produces:
  - `POST /api/v1/auth/invites` (existing, `manage_community`) now accepts optional body `{"roleId": string}`; response gains `"role_id"`. Empty body still works (defaults to `Member`). Granting `Owner` via invite requires the actor to be an Owner.
  - `GET /api/v1/admin/invites` (`manage_community`) → `[{"id","creator","roleId","createdAt","expiresAt"}]` (pending only: unused, unexpired; `id` = token hash).
  - `DELETE /api/v1/admin/invites/{id}` (`manage_community`) — deletes an unused invite.
  - `handleAcceptInvite` assigns the invite's `role_id` (fallback `Member`).

- [ ] **Step 1: Modify `handleCreateInvite`**

```go
func handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	roleID := "Member"
	var body struct {
		RoleID string `json:"roleId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.RoleID != "" {
		roleID = body.RoleID
	}
	if roleID == "Owner" && !containsRole(user.Roles, "Owner") {
		http.Error(w, "Forbidden: only an Owner can create Owner invites", http.StatusForbidden)
		return
	}
	var exists bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1)`, roleID).Scan(&exists); err != nil || !exists {
		http.Error(w, "Bad Request: unknown role", http.StatusBadRequest)
		return
	}
	rawToken, tokenHash := generateToken()
	expiry := time.Now().Add(7 * 24 * time.Hour)
	_, err := dbPool.Exec(r.Context(), `
		INSERT INTO invites (token_hash, creator_id, expires_at, role_id) VALUES ($1, $2, $3, $4)
	`, tokenHash, user.ID, expiry, roleID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"invite_token": rawToken,
		"expires_at":   expiry.Format(time.RFC3339),
		"role_id":      roleID,
	})
}
```

(Decode error is ignored on purpose — the old client sends no body.)

- [ ] **Step 2: Modify `handleAcceptInvite`**

Extend the invite SELECT to include the role:

```go
	var inviteRole *string
	err := dbPool.QueryRow(ctx, `
		SELECT expires_at, used_at, role_id FROM invites WHERE token_hash = $1
	`, tokenHash).Scan(&expiresAt, &usedAt, &inviteRole)
```

and replace the hardcoded `'Member'` insert:

```go
	assignRole := "Member"
	if inviteRole != nil && *inviteRole != "" {
		assignRole = *inviteRole
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
	`, userID, assignRole)
```

- [ ] **Step 3: Add list/revoke handlers** (`settings.go`)

```go
type InviteResponse struct {
	ID        string `json:"id"`
	Creator   string `json:"creator"`
	RoleID    string `json:"roleId"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

func handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT i.token_hash, COALESCE(u.username, '(deleted)'), COALESCE(i.role_id, 'Member'),
			i.created_at, i.expires_at
		FROM invites i
		LEFT JOIN users u ON i.creator_id = u.id
		WHERE i.used_at IS NULL AND i.expires_at > NOW()
		ORDER BY i.created_at DESC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	invites := []InviteResponse{}
	for rows.Next() {
		var inv InviteResponse
		var created, expires time.Time
		if err := rows.Scan(&inv.ID, &inv.Creator, &inv.RoleID, &created, &expires); err != nil {
			continue
		}
		inv.CreatedAt = created.Format(time.RFC3339)
		inv.ExpiresAt = expires.Format(time.RFC3339)
		invites = append(invites, inv)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(invites)
}

func handleAdminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tag, err := dbPool.Exec(r.Context(), `
		DELETE FROM invites WHERE token_hash = $1 AND used_at IS NULL
	`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "Invite not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
```

Routes:

```go
	mux.Handle("GET /api/v1/admin/invites", withAuth(http.HandlerFunc(handleAdminListInvites), "manage_community"))
	mux.Handle("DELETE /api/v1/admin/invites/{id}", withAuth(http.HandlerFunc(handleAdminRevokeInvite), "manage_community"))
```

- [ ] **Step 4: Run backend gate** — PASS + vet clean.

---

### Task 8: Admin — role & permission editor; chat message enrichment

**Files:**
- Modify: `backend/settings.go`, `backend/main.go` (routes; `WSMessage` type ~line 74; `getMessagesForChannel` ~line 1257; live-message construction ~line 1198), `backend/settings_test.go`

**Interfaces:**
- Produces routes (all `manage_roles`):
  - `GET /api/v1/admin/roles` → `[{"id","name","builtin","memberCount","permissions":[...]}]`
  - `GET /api/v1/admin/permissions` → `[{"id","description"}]`
  - `POST /api/v1/admin/roles` — `{"name": string, "permissions": [...]}` → 201 with the new role object; id from `slugifyRoleID`.
  - `PUT /api/v1/admin/roles/{id}/permissions` — `{"permissions":[...]}`; **403 for `Owner`** (immutable).
  - `DELETE /api/v1/admin/roles/{id}` — **only non-builtin** roles; cascades `user_roles`/`role_permissions`.
- Produces: `WSMessage` gains `SenderID string \`json:"senderId"\``, `DisplayName string \`json:"displayName"\``, `AvatarUrl string \`json:"avatarUrl"\`` (empty when the user has no avatar). Frontend Task 12 consumes these exact names.

- [ ] **Step 1: Role handlers** (`settings.go`)

```go
type RoleResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Builtin     bool     `json:"builtin"`
	MemberCount int      `json:"memberCount"`
	Permissions []string `json:"permissions"`
}

func handleAdminListRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT ro.id, ro.name, ro.builtin,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = ro.id),
			COALESCE((SELECT array_agg(rp.permission_id) FROM role_permissions rp WHERE rp.role_id = ro.id), '{}')
		FROM roles ro
		ORDER BY ro.builtin DESC, ro.id ASC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	roles := []RoleResponse{}
	for rows.Next() {
		var role RoleResponse
		if err := rows.Scan(&role.ID, &role.Name, &role.Builtin, &role.MemberCount, &role.Permissions); err != nil {
			continue
		}
		roles = append(roles, role)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func handleAdminListPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `SELECT id, COALESCE(description, '') FROM permissions ORDER BY id`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type perm struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	perms := []perm{}
	for rows.Next() {
		var p perm
		if err := rows.Scan(&p.ID, &p.Description); err == nil {
			perms = append(perms, p)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(perms)
}

func setRolePermissions(ctx context.Context, roleID string, perms []string) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, roleID, p); err != nil {
			return fmt.Errorf("unknown permission %q", p)
		}
	}
	return tx.Commit(ctx)
}

func handleAdminCreateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	id, err := slugifyRoleID(body.Name)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err := dbPool.Exec(ctx, `
		INSERT INTO roles (id, name, builtin) VALUES ($1, $2, FALSE)
	`, id, strings.TrimSpace(body.Name)); err != nil {
		http.Error(w, "Conflict: role already exists", http.StatusConflict)
		return
	}
	if len(body.Permissions) > 0 {
		if err := setRolePermissions(ctx, id, body.Permissions); err != nil {
			http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(RoleResponse{ID: id, Name: strings.TrimSpace(body.Name), Permissions: body.Permissions})
}

func handleAdminSetRolePermissions(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("id")
	if roleID == "Owner" {
		http.Error(w, "Forbidden: the Owner role is immutable", http.StatusForbidden)
		return
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	var exists bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1)`, roleID).Scan(&exists); err != nil || !exists {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}
	if err := setRolePermissions(r.Context(), roleID, body.Permissions); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminDeleteRole(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("id")
	var builtin bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT builtin FROM roles WHERE id = $1`, roleID).Scan(&builtin); err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}
	if builtin {
		http.Error(w, "Forbidden: built-in roles cannot be deleted", http.StatusForbidden)
		return
	}
	if _, err := dbPool.Exec(r.Context(), `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
```

Routes:

```go
	mux.Handle("GET /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminListRoles), "manage_roles"))
	mux.Handle("GET /api/v1/admin/permissions", withAuth(http.HandlerFunc(handleAdminListPermissions), "manage_roles"))
	mux.Handle("POST /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminCreateRole), "manage_roles"))
	mux.Handle("PUT /api/v1/admin/roles/{id}/permissions", withAuth(http.HandlerFunc(handleAdminSetRolePermissions), "manage_roles"))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", withAuth(http.HandlerFunc(handleAdminDeleteRole), "manage_roles"))
```

- [ ] **Step 2: Chat enrichment.** Extend `WSMessage` in `main.go`:

```go
type WSMessage struct {
	ID          string `json:"id"`
	Sender      string `json:"sender"`
	SenderID    string `json:"senderId"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	AvatarUrl   string `json:"avatarUrl"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Timestamp   string `json:"timestamp"`
}
```

In `getMessagesForChannel`, widen the SELECT and populate the new fields:

```go
		SELECT m.id::text, u.id::text, u.username, COALESCE(u.display_name, ''),
			u.avatar_file_id IS NOT NULL, m.content, m.created_at
		FROM messages m
		JOIN users u ON m.user_id = u.id
		WHERE m.channel_id = $1
		ORDER BY m.created_at DESC
		LIMIT 50
```

```go
		var hasAvatar bool
		if err := rows.Scan(&msg.ID, &msg.SenderID, &msg.Sender, &msg.DisplayName, &hasAvatar, &msg.Content, &t); err != nil {
			return nil, err
		}
		if hasAvatar {
			msg.AvatarUrl = "/api/v1/users/" + msg.SenderID + "/avatar"
		}
```

In the live-message path (~line 1174), widen the sender lookup:

```go
		var displayName string
		var hasAvatar bool
		err = dbPool.QueryRow(ctx, `
			SELECT username, COALESCE(display_name, ''), avatar_file_id IS NOT NULL
			FROM users WHERE id = $1
		`, userID).Scan(&username, &displayName, &hasAvatar)
```

and set on the broadcast message: `SenderID: userID`, `DisplayName: displayName`, `AvatarUrl:` same construction as above.

- [ ] **Step 3: Run backend gate** — PASS + vet clean. This completes the backend; do a final read-through of the `main.go` diff for accidental behavior changes to existing handlers.

---

### Task 9: Frontend foundations — route, styles, stores, shell wiring

**Files:**
- Create: `frontend/src/styles/settings.css`
- Create: `frontend/src/app/(shell)/settings/page.tsx`
- Create: `frontend/src/stores/usePreferencesStore.ts`
- Modify: `frontend/src/app/layout.tsx` (import settings.css)
- Modify: `frontend/src/stores/useAuthStore.ts` (`CurrentUser` fields)
- Modify: `frontend/src/components/AppShell.tsx` (gear icon → Link, avatar chip, theme toggle write-through)

**Interfaces:**
- Consumes: backend routes from Tasks 3–8; `api()`/`apiBase()` from `lib/api.ts`; `useThemeStore`.
- Produces: `CurrentUser{ID, Username, Roles, DisplayName, HasAvatar}` (matches Task 1 marshaling); `usePreferencesStore` with `{prefs: {theme, sceneEnabled, reducedMotion}, loaded, load(), save(partial)}`; section components contract for Task 10–12: each is a client component taking no props, default-styled by `settings.css` classes (`.settings`, `.setnav`, `.setpanel`, `.setrow`, `.settable`).
- Produces: `avatarUrlFor(userId: string): string` helper exported from `usePreferencesStore.ts`? **No** — put it in `lib/api.ts`: `export function avatarUrl(userId: string): string { return `${apiBase()}/api/v1/users/${userId}/avatar`; }`.

- [ ] **Step 1: Extend `CurrentUser`** in `useAuthStore.ts`:

```ts
export interface CurrentUser {
  ID: string;
  Username: string;
  Roles: string[];
  DisplayName: string;
  HasAvatar: boolean;
}
```

Add to `lib/api.ts`:

```ts
export function avatarUrl(userId: string): string {
  return `${apiBase()}/api/v1/users/${userId}/avatar`;
}
```

- [ ] **Step 2: Create `usePreferencesStore.ts`**

```ts
import { create } from "zustand";
import { api } from "@/lib/api";
import { useThemeStore, type Theme } from "@/stores/useThemeStore";

export interface Prefs {
  theme: Theme;
  sceneEnabled: boolean;
  reducedMotion: boolean;
}

const DEFAULTS: Prefs = { theme: "synthwave", sceneEnabled: true, reducedMotion: false };

interface PreferencesState {
  prefs: Prefs;
  loaded: boolean;
  load: () => Promise<void>;
  save: (patch: Partial<Prefs>) => Promise<void>;
}

function applySideEffects(prefs: Prefs) {
  useThemeStore.getState().setTheme(prefs.theme);
  if (typeof document !== "undefined") {
    document.documentElement.dataset.motion = prefs.reducedMotion ? "reduced" : "full";
  }
}

export const usePreferencesStore = create<PreferencesState>()((set, get) => ({
  prefs: DEFAULTS,
  loaded: false,

  load: async () => {
    try {
      const remote = await api<Partial<Prefs>>("/api/v1/users/me/preferences");
      const prefs = { ...DEFAULTS, ...remote };
      set({ prefs, loaded: true });
      applySideEffects(prefs);
    } catch {
      set({ loaded: true }); // offline/mock mode: defaults + local theme persist
    }
  },

  save: async (patch) => {
    const prefs = { ...get().prefs, ...patch };
    set({ prefs });
    applySideEffects(prefs);
    try {
      await api("/api/v1/users/me/preferences", {
        method: "PUT",
        body: JSON.stringify(prefs),
      });
    } catch {
      // Server persistence is best-effort; the UI already applied the change.
    }
  },
}));
```

- [ ] **Step 3: Wire AppShell.** In `AppShell.tsx`:
  - Import `Link` is already there; replace the dead gear button with:

```tsx
          <Link href="/settings" className={`iconbtn${pathname.startsWith("/settings") ? " on" : ""}`} aria-label="settings">
            <Settings size={18} />
          </Link>
```

  - Load prefs once authenticated (new effect):

```tsx
  const prefsLoaded = usePreferencesStore((s) => s.loaded);
  const loadPrefs = usePreferencesStore((s) => s.load);
  useEffect(() => {
    if (status === "authenticated" && !prefsLoaded) void loadPrefs();
  }, [status, prefsLoaded, loadPrefs]);
```

  - Theme quick-toggle writes through to the server: replace `onClick={toggleTheme}` with

```tsx
            onClick={() =>
              void usePreferencesStore
                .getState()
                .save({ theme: theme === "synthwave" ? "ink" : "synthwave" })
            }
```

(`toggleTheme` import can be dropped if now unused.)

  - Avatar in the user chip:

```tsx
          <span className="chip">
            {user?.HasAvatar ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img className="chipav" src={avatarUrl(user.ID)} alt="" />
            ) : (
              <span className="dot" />
            )}{" "}
            {user?.DisplayName || user?.Username}
          </span>
```

- [ ] **Step 4: Create `settings.css`** (import in `app/layout.tsx` after `stream.css`):

```css
/* Settings module — Telos Design System */
.settings{display:flex;gap:26px;height:100%;min-height:0;padding:26px}
.setnav{width:210px;flex:none;display:flex;flex-direction:column;gap:4px}
.setnav .grouplabel{margin:14px 0 6px}
.setnavbtn{display:flex;align-items:center;gap:10px;padding:9px 12px;border-radius:var(--r-md);
  font-family:var(--f-sans);font-weight:600;font-size:13.5px;color:var(--muted);background:none;border:0;
  cursor:pointer;text-align:left;width:100%}
.setnavbtn:hover{background:var(--surface-2);color:var(--text)}
.setnavbtn.on{background:var(--surface-2);color:var(--accent);box-shadow:inset 0 0 0 1px var(--line)}
.setpanel{flex:1;min-width:0;overflow-y:auto;display:flex;flex-direction:column;gap:18px;max-width:760px}
.setpanel h2{font-size:19px;margin:0}
.setcard{background:var(--surface-2);border:1px solid var(--line);border-radius:var(--r-lg);padding:20px;
  display:flex;flex-direction:column;gap:14px}
.setrow{display:flex;align-items:center;justify-content:space-between;gap:14px}
.setrow .lbl{display:flex;flex-direction:column;gap:3px}
.setrow .lbl b{font-size:13.5px}
.setrow .lbl span{font-size:12px;color:var(--faint)}
.setfield{display:flex;flex-direction:column;gap:6px}
.setfield label{font-family:var(--f-mono);font-size:11px;letter-spacing:.18em;text-transform:uppercase;color:var(--faint)}
.setfield input,.setfield select{background:var(--surface-1);border:1px solid var(--line);border-radius:var(--r-md);
  padding:10px 12px;color:var(--text);font-family:var(--f-sans);font-size:13.5px}
.setfield input:focus,.setfield select:focus{outline:none;border-color:var(--accent)}
.settable{width:100%;border-collapse:collapse;font-size:13px}
.settable th{font-family:var(--f-mono);font-size:10.5px;letter-spacing:.14em;text-transform:uppercase;
  color:var(--faint);text-align:left;padding:8px 10px;border-bottom:1px solid var(--line)}
.settable td{padding:10px;border-bottom:1px solid var(--line);vertical-align:middle}
.setava{width:64px;height:64px;border-radius:var(--r-pill);object-fit:cover;border:1px solid var(--line)}
.chipav{width:18px;height:18px;border-radius:var(--r-pill);object-fit:cover}
.msgav{width:100%;height:100%;border-radius:inherit;object-fit:cover;display:block}
.setmsg{font-family:var(--f-mono);font-size:12px}
.setmsg.ok{color:var(--live)}
.setmsg.err{color:var(--rose)}
.setswitch{appearance:none;width:38px;height:22px;border-radius:var(--r-pill);background:var(--surface-1);
  border:1px solid var(--line);position:relative;cursor:pointer;flex:none}
.setswitch::after{content:"";position:absolute;top:2px;left:2px;width:16px;height:16px;border-radius:50%;
  background:var(--muted);transition:transform .15s ease,background .15s ease}
.setswitch:checked{background:var(--primary)}
.setswitch:checked::after{transform:translateX(16px);background:var(--on-primary)}
.setperms{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:8px}
.setperm{display:flex;align-items:flex-start;gap:8px;font-size:12.5px;padding:8px;border-radius:var(--r-md);
  border:1px solid var(--line)}
.setperm .desc{color:var(--faint);font-size:11.5px}
.setdanger{border-color:color-mix(in srgb, var(--rose) 45%, var(--line))}
html[data-motion="reduced"] *,html[data-motion="reduced"] *::before,html[data-motion="reduced"] *::after{
  animation-duration:.001s!important;transition-duration:.001s!important}
@media (max-width: 900px){.settings{flex-direction:column;padding:16px}.setnav{width:100%;flex-direction:row;flex-wrap:wrap}}
```

(Verify token names — `--surface-1`, `--surface-2`, `--line`, `--muted`, `--faint`, `--accent`, `--rose`, `--live`, `--primary`, `--on-primary`, `--r-md`, `--r-lg`, `--r-pill`, `--f-mono`, `--f-sans` — against `frontend/src/styles/tokens/themes.css` and `scale.css` before use; substitute the real names if any differ.)

- [ ] **Step 5: Create the page skeleton** (`app/(shell)/settings/page.tsx`) — sections render placeholder cards until Tasks 10–12 fill them:

```tsx
"use client";

import { useState } from "react";
import {
  KeyRound,
  Mail,
  Palette,
  Shield,
  User,
  Users,
} from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { ProfileSection } from "@/components/settings/ProfileSection";
import { SecuritySection } from "@/components/settings/SecuritySection";
import { AppearanceSection } from "@/components/settings/AppearanceSection";
import { AdminUsersSection } from "@/components/settings/AdminUsersSection";
import { AdminInvitesSection } from "@/components/settings/AdminInvitesSection";
import { AdminRolesSection } from "@/components/settings/AdminRolesSection";

const SECTIONS = [
  { id: "profile", label: "Profile", icon: User, admin: false, C: ProfileSection },
  { id: "security", label: "Security", icon: Shield, admin: false, C: SecuritySection },
  { id: "appearance", label: "Appearance", icon: Palette, admin: false, C: AppearanceSection },
  { id: "users", label: "Members", icon: Users, admin: true, C: AdminUsersSection },
  { id: "invites", label: "Invites", icon: Mail, admin: true, C: AdminInvitesSection },
  { id: "roles", label: "Roles", icon: KeyRound, admin: true, C: AdminRolesSection },
] as const;

export default function SettingsPage() {
  const user = useAuthStore((s) => s.user);
  const [active, setActive] = useState<string>("profile");
  // Client-side visibility only — every admin endpoint enforces permissions server-side.
  const isAdmin =
    user?.Roles.some((r) => r === "Owner" || r === "Administrator") ?? false;
  const visible = SECTIONS.filter((s) => !s.admin || isAdmin);
  const current = visible.find((s) => s.id === active) ?? visible[0];
  const Panel = current.C;

  return (
    <div className="settings" data-testid="settings-page">
      <nav className="setnav">
        <h3 className="grouplabel">User settings</h3>
        {visible
          .filter((s) => !s.admin)
          .map(({ id, label, icon: Icon }) => (
            <button key={id} className={`setnavbtn${current.id === id ? " on" : ""}`} onClick={() => setActive(id)}>
              <Icon size={16} /> {label}
            </button>
          ))}
        {isAdmin && (
          <>
            <h3 className="grouplabel">Administration</h3>
            {visible
              .filter((s) => s.admin)
              .map(({ id, label, icon: Icon }) => (
                <button key={id} className={`setnavbtn${current.id === id ? " on" : ""}`} onClick={() => setActive(id)}>
                  <Icon size={16} /> {label}
                </button>
              ))}
          </>
        )}
      </nav>
      <div className="setpanel">
        <Panel />
      </div>
    </div>
  );
}
```

For this task, create the six section files as minimal stubs so the build passes (each replaced in Tasks 10–12), e.g.:

```tsx
"use client";
export function ProfileSection() {
  return <h2>Profile</h2>;
}
```

- [ ] **Step 6: Verify** — from `frontend/`: `npm run lint && npm run build`. Expected: both pass; `out/` contains `settings/index.html` (or `settings.html` per export config).

---

### Task 10: Profile & Security sections

**Files:**
- Rewrite: `frontend/src/components/settings/ProfileSection.tsx`
- Rewrite: `frontend/src/components/settings/SecuritySection.tsx`

**Interfaces:**
- Consumes: `PATCH /api/v1/users/me`, `POST/DELETE /api/v1/users/me/avatar`, `GET /api/v1/users/{id}/avatar`, `POST /api/v1/users/me/password`, `GET/DELETE /api/v1/users/me/sessions*`; `useAuthStore.fetchMe` to refresh after profile changes; `avatarUrl()` from `lib/api.ts`.

- [ ] **Step 1: ProfileSection**

```tsx
"use client";

import { useRef, useState } from "react";
import { api, apiBase, avatarUrl, ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";

export function ProfileSection() {
  const { user, fetchMe } = useAuthStore();
  const [displayName, setDisplayName] = useState(user?.DisplayName ?? "");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [cacheBust, setCacheBust] = useState(0);

  const saveName = async () => {
    setBusy(true);
    setMsg(null);
    try {
      await api("/api/v1/users/me", {
        method: "PATCH",
        body: JSON.stringify({ displayName }),
      });
      await fetchMe();
      setMsg({ ok: true, text: "Profile saved." });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Save failed." });
    } finally {
      setBusy(false);
    }
  };

  const uploadAvatar = async (file: File) => {
    setBusy(true);
    setMsg(null);
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await fetch(`${apiBase()}/api/v1/users/me/avatar`, {
        method: "POST",
        credentials: "include",
        body: form, // multipart — do NOT set Content-Type manually
      });
      if (!res.ok) throw new ApiError(res.status, (await res.text()).trim());
      await fetchMe();
      setCacheBust(Date.now());
      setMsg({ ok: true, text: "Avatar updated (scanned clean)." });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Upload failed." });
    } finally {
      setBusy(false);
    }
  };

  const removeAvatar = async () => {
    await api("/api/v1/users/me/avatar", { method: "DELETE" });
    await fetchMe();
    setCacheBust(Date.now());
  };

  if (!user) return null;
  return (
    <>
      <h2>Profile</h2>
      <div className="setcard">
        <div className="setrow">
          {user.HasAvatar ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img className="setava" src={`${avatarUrl(user.ID)}?v=${cacheBust}`} alt="Your avatar" />
          ) : (
            <div className="setava" style={{ display: "grid", placeItems: "center" }}>
              {user.Username.slice(0, 2).toUpperCase()}
            </div>
          )}
          <div style={{ display: "flex", gap: 8 }}>
            <button className="btn-ghost btn-sm" disabled={busy} onClick={() => fileRef.current?.click()}>
              Upload avatar
            </button>
            {user.HasAvatar && (
              <button className="btn-ghost btn-sm" disabled={busy} onClick={() => void removeAvatar()}>
                Remove
              </button>
            )}
          </div>
          <input
            ref={fileRef}
            type="file"
            accept=".jpg,.jpeg,.png,.webp"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void uploadAvatar(f);
              e.target.value = "";
            }}
          />
        </div>
        <div className="setfield">
          <label htmlFor="set-displayname">Display name</label>
          <input
            id="set-displayname"
            value={displayName}
            maxLength={64}
            placeholder={user.Username}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="setfield">
          <label>Username</label>
          <input value={user.Username} disabled />
        </div>
        <div className="setrow">
          <button className="btn cyan btn-sm" disabled={busy} onClick={() => void saveName()}>
            Save profile
          </button>
          {msg && <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>}
        </div>
      </div>
    </>
  );
}
```

- [ ] **Step 2: SecuritySection**

```tsx
"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface SessionInfo {
  id: string;
  createdAt: string;
  lastSeenAt: string | null;
  userAgent: string;
  clientIp: string;
  current: boolean;
}

export function SecuritySection() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);

  const loadSessions = useCallback(async () => {
    try {
      setSessions(await api<SessionInfo[]>("/api/v1/users/me/sessions"));
    } catch {
      setSessions([]);
    }
  }, []);

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  const changePassword = async () => {
    setMsg(null);
    if (newPassword !== confirm) {
      setMsg({ ok: false, text: "New passwords do not match." });
      return;
    }
    if (newPassword.length < 15) {
      setMsg({ ok: false, text: "Password must be at least 15 characters." });
      return;
    }
    try {
      await api("/api/v1/users/me/password", {
        method: "POST",
        body: JSON.stringify({ currentPassword, newPassword }),
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirm("");
      setMsg({ ok: true, text: "Password changed. Other sessions were signed out." });
      void loadSessions();
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Change failed." });
    }
  };

  const revoke = async (id: string) => {
    await api(`/api/v1/users/me/sessions/${id}`, { method: "DELETE" });
    void loadSessions();
  };

  const revokeOthers = async () => {
    await api("/api/v1/users/me/sessions/revoke-others", { method: "POST" });
    void loadSessions();
  };

  return (
    <>
      <h2>Security</h2>
      <div className="setcard">
        <div className="setfield">
          <label htmlFor="set-curpw">Current password</label>
          <input id="set-curpw" type="password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} autoComplete="current-password" />
        </div>
        <div className="setfield">
          <label htmlFor="set-newpw">New password (15+ characters)</label>
          <input id="set-newpw" type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} autoComplete="new-password" />
        </div>
        <div className="setfield">
          <label htmlFor="set-confpw">Confirm new password</label>
          <input id="set-confpw" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
        </div>
        <div className="setrow">
          <button className="btn cyan btn-sm" onClick={() => void changePassword()}>
            Change password
          </button>
          {msg && <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>}
        </div>
      </div>

      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Active sessions</b>
            <span>Devices currently signed in to your account.</span>
          </div>
          <button className="btn-ghost btn-sm" onClick={() => void revokeOthers()}>
            Sign out everywhere else
          </button>
        </div>
        <table className="settable">
          <thead>
            <tr>
              <th>Device</th>
              <th>IP</th>
              <th>Last seen</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.id}>
                <td>
                  {s.userAgent || "Unknown device"} {s.current && <span className="tag">this device</span>}
                </td>
                <td className="mono">{s.clientIp || "—"}</td>
                <td>{new Date(s.lastSeenAt ?? s.createdAt).toLocaleString()}</td>
                <td>
                  {!s.current && (
                    <button className="btn-ghost btn-sm" onClick={() => void revoke(s.id)}>
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
```

- [ ] **Step 3: Verify** — `npm run lint && npm run build` pass.

---

### Task 11: Appearance section & scene/motion wiring

**Files:**
- Rewrite: `frontend/src/components/settings/AppearanceSection.tsx`
- Modify: `frontend/src/components/VaporwaveScene.tsx` (early-return when scene disabled)

**Interfaces:**
- Consumes: `usePreferencesStore` (Task 9), `useThemeStore`.

- [ ] **Step 1: AppearanceSection**

```tsx
"use client";

import { usePreferencesStore } from "@/stores/usePreferencesStore";

export function AppearanceSection() {
  const prefs = usePreferencesStore((s) => s.prefs);
  const save = usePreferencesStore((s) => s.save);

  return (
    <>
      <h2>Appearance</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Theme</b>
            <span>Synced to your account across devices.</span>
          </div>
          <select
            value={prefs.theme}
            onChange={(e) => void save({ theme: e.target.value as "synthwave" | "ink" })}
            aria-label="theme"
          >
            <option value="synthwave">Synthwave</option>
            <option value="ink">Ink</option>
          </select>
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Background scene</b>
            <span>The animated vaporwave backdrop on module pages.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            checked={prefs.sceneEnabled}
            onChange={(e) => void save({ sceneEnabled: e.target.checked })}
            aria-label="background scene"
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Reduce motion</b>
            <span>Minimize animations and transitions across the app.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            checked={prefs.reducedMotion}
            onChange={(e) => void save({ reducedMotion: e.target.checked })}
            aria-label="reduce motion"
          />
        </div>
      </div>
    </>
  );
}
```

- [ ] **Step 2: Gate `VaporwaveScene`.** Read the component first, then add at the top of its function body:

```tsx
  const sceneEnabled = usePreferencesStore((s) => s.prefs.sceneEnabled);
  if (!sceneEnabled) return null;
```

(with the import; respect any hooks-order constraints — if the component has other hooks, place this hook first and the early return **after all hooks**.)

- [ ] **Step 3: Verify** — `npm run lint && npm run build` pass. Note: the `select` element uses `.setfield select` styling only inside `.setfield`; wrap or extend `settings.css` if it renders unstyled (add `.setrow select{...}` mirroring `.setfield input`).

---

### Task 12: Admin sections (Members, Invites, Roles) & chat avatars

**Files:**
- Rewrite: `frontend/src/components/settings/AdminUsersSection.tsx`, `AdminInvitesSection.tsx`, `AdminRolesSection.tsx`
- Modify: `frontend/src/app/(shell)/chat/page.tsx` (avatar/display-name rendering ~line 65)
- Modify: whatever type declares chat messages client-side (check `useChatSessionStore.ts` for the message interface) — add `senderId`, `displayName`, `avatarUrl` optional fields.

**Interfaces:**
- Consumes: all `/api/v1/admin/*` routes (Tasks 6–8); enriched `WSMessage` fields `senderId`/`displayName`/`avatarUrl` (Task 8); `avatarUrl()` helper.

- [ ] **Step 1: AdminUsersSection**

```tsx
"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useAuthStore } from "@/stores/useAuthStore";

interface AdminUser {
  id: string;
  username: string;
  displayName: string;
  active: boolean;
  createdAt: string;
  roles: string[];
  hasAvatar: boolean;
}

interface RoleInfo {
  id: string;
  name: string;
  builtin: boolean;
  memberCount: number;
  permissions: string[];
}

export function AdminUsersSection() {
  const me = useAuthStore((s) => s.user);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [u, r] = await Promise.all([
        api<AdminUser[]>("/api/v1/admin/users"),
        api<RoleInfo[]>("/api/v1/admin/roles"),
      ]);
      setUsers(u);
      setRoles(r);
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Failed to load members.");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const run = async (fn: () => Promise<unknown>) => {
    setMsg(null);
    try {
      await fn();
      await load();
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Action failed.");
    }
  };

  const toggleRole = (u: AdminUser, roleId: string) => {
    const next = u.roles.includes(roleId)
      ? u.roles.filter((r) => r !== roleId)
      : [...u.roles, roleId];
    void run(() =>
      api(`/api/v1/admin/users/${u.id}/roles`, {
        method: "PUT",
        body: JSON.stringify({ roles: next }),
      }),
    );
  };

  return (
    <>
      <h2>Members</h2>
      {msg && <span className="setmsg err">{msg}</span>}
      <div className="setcard">
        <table className="settable">
          <thead>
            <tr>
              <th>Member</th>
              <th>Roles</th>
              <th>Status</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>
                  <b>{u.displayName || u.username}</b>{" "}
                  <span className="mono" style={{ opacity: 0.6 }}>
                    @{u.username}
                  </span>
                </td>
                <td>
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                    {roles.map((r) => (
                      <button
                        key={r.id}
                        className={`chip${u.roles.includes(r.id) ? " on" : ""}`}
                        disabled={u.id === me?.ID}
                        title={u.id === me?.ID ? "You cannot change your own roles" : `Toggle ${r.name}`}
                        onClick={() => toggleRole(u, r.id)}
                      >
                        {r.name}
                      </button>
                    ))}
                  </div>
                </td>
                <td>{u.active ? <span className="tag">active</span> : <span className="tag">disabled</span>}</td>
                <td>
                  {u.id !== me?.ID && (
                    <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
                      <button
                        className="btn-ghost btn-sm"
                        onClick={() =>
                          void run(() =>
                            api(`/api/v1/admin/users/${u.id}/active`, {
                              method: "POST",
                              body: JSON.stringify({ active: !u.active }),
                            }),
                          )
                        }
                      >
                        {u.active ? "Disable" : "Enable"}
                      </button>
                      {confirmDelete === u.id ? (
                        <button
                          className="btn rose btn-sm"
                          onClick={() => {
                            setConfirmDelete(null);
                            void run(() => api(`/api/v1/admin/users/${u.id}`, { method: "DELETE" }));
                          }}
                        >
                          Confirm delete
                        </button>
                      ) : (
                        <button className="btn-ghost btn-sm" onClick={() => setConfirmDelete(u.id)}>
                          Delete
                        </button>
                      )}
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <span className="setmsg" style={{ color: "var(--faint)" }}>
          Deleting a member anonymizes the account; their messages remain attributed to “Deleted User”.
        </span>
      </div>
    </>
  );
}
```

- [ ] **Step 2: AdminInvitesSection**

```tsx
"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface Invite {
  id: string;
  creator: string;
  roleId: string;
  createdAt: string;
  expiresAt: string;
}

interface RoleInfo {
  id: string;
  name: string;
}

export function AdminInvitesSection() {
  const [invites, setInvites] = useState<Invite[]>([]);
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [roleId, setRoleId] = useState("Member");
  const [newToken, setNewToken] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [inv, r] = await Promise.all([
        api<Invite[]>("/api/v1/admin/invites"),
        api<RoleInfo[]>("/api/v1/admin/roles"),
      ]);
      setInvites(inv);
      setRoles(r);
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Failed to load invites.");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const createInvite = async () => {
    setMsg(null);
    try {
      const res = await api<{ invite_token: string }>("/api/v1/auth/invites", {
        method: "POST",
        body: JSON.stringify({ roleId }),
      });
      setNewToken(res.invite_token);
      void load();
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Invite creation failed.");
    }
  };

  const revoke = async (id: string) => {
    await api(`/api/v1/admin/invites/${id}`, { method: "DELETE" });
    void load();
  };

  return (
    <>
      <h2>Invites</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Create invite</b>
            <span>Single-use, valid for 7 days. The new member joins with the selected role.</span>
          </div>
          <div style={{ display: "flex", gap: 8 }}>
            <select value={roleId} onChange={(e) => setRoleId(e.target.value)} aria-label="invite role">
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
            <button className="btn cyan btn-sm" onClick={() => void createInvite()}>
              Generate
            </button>
          </div>
        </div>
        {newToken && (
          <div className="setrow">
            <input className="mono" readOnly value={newToken} style={{ flex: 1 }} onFocus={(e) => e.target.select()} />
            <button className="btn-ghost btn-sm" onClick={() => void navigator.clipboard.writeText(newToken)}>
              Copy
            </button>
          </div>
        )}
        {msg && <span className="setmsg err">{msg}</span>}
      </div>

      <div className="setcard">
        <b>Pending invites</b>
        <table className="settable">
          <thead>
            <tr>
              <th>Created by</th>
              <th>Role</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {invites.map((i) => (
              <tr key={i.id}>
                <td>{i.creator}</td>
                <td>
                  <span className="tag">{i.roleId}</span>
                </td>
                <td>{new Date(i.expiresAt).toLocaleString()}</td>
                <td>
                  <button className="btn-ghost btn-sm" onClick={() => void revoke(i.id)}>
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
            {invites.length === 0 && (
              <tr>
                <td colSpan={4} style={{ color: "var(--faint)" }}>
                  No pending invites.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
```

Note: raw invite tokens are shown only once at creation (the server stores hashes) — the pending table intentionally has no token column.

- [ ] **Step 3: AdminRolesSection**

```tsx
"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";

interface RoleInfo {
  id: string;
  name: string;
  builtin: boolean;
  memberCount: number;
  permissions: string[];
}

interface PermInfo {
  id: string;
  description: string;
}

export function AdminRolesSection() {
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [perms, setPerms] = useState<PermInfo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [draft, setDraft] = useState<string[]>([]);
  const [newName, setNewName] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [r, p] = await Promise.all([
        api<RoleInfo[]>("/api/v1/admin/roles"),
        api<PermInfo[]>("/api/v1/admin/permissions"),
      ]);
      setRoles(r);
      setPerms(p);
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Failed to load roles." });
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const role = roles.find((r) => r.id === selected) ?? null;
  const editable = role !== null && role.id !== "Owner";

  const select = (r: RoleInfo) => {
    setSelected(r.id);
    setDraft(r.permissions);
    setMsg(null);
  };

  const savePerms = async () => {
    if (!role) return;
    try {
      await api(`/api/v1/admin/roles/${role.id}/permissions`, {
        method: "PUT",
        body: JSON.stringify({ permissions: draft }),
      });
      setMsg({ ok: true, text: "Permissions saved." });
      void load();
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Save failed." });
    }
  };

  const createRole = async () => {
    try {
      await api("/api/v1/admin/roles", {
        method: "POST",
        body: JSON.stringify({ name: newName, permissions: [] }),
      });
      setNewName("");
      void load();
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Create failed." });
    }
  };

  const deleteRole = async () => {
    if (!role) return;
    try {
      await api(`/api/v1/admin/roles/${role.id}`, { method: "DELETE" });
      setSelected(null);
      void load();
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : "Delete failed." });
    }
  };

  return (
    <>
      <h2>Roles &amp; permissions</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="setfield" style={{ flex: 1 }}>
            <label htmlFor="set-newrole">New role name</label>
            <input id="set-newrole" value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="e.g. Book Club" />
          </div>
          <button className="btn cyan btn-sm" disabled={newName.trim().length < 2} onClick={() => void createRole()}>
            Create role
          </button>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {roles.map((r) => (
            <button key={r.id} className={`chip${selected === r.id ? " on" : ""}`} onClick={() => select(r)}>
              {r.name} · {r.memberCount}
            </button>
          ))}
        </div>
      </div>

      {role && (
        <div className={`setcard${!role.builtin ? " setdanger" : ""}`}>
          <div className="setrow">
            <div className="lbl">
              <b>{role.name}</b>
              <span>
                {role.id === "Owner"
                  ? "The Owner role always has every permission and cannot be edited."
                  : role.builtin
                    ? "Built-in role — permissions editable, role not deletable."
                    : "Custom role."}
              </span>
            </div>
            {!role.builtin && (
              <button className="btn-ghost btn-sm" onClick={() => void deleteRole()}>
                Delete role
              </button>
            )}
          </div>
          <div className="setperms">
            {perms.map((p) => (
              <label key={p.id} className="setperm">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={role.id === "Owner" ? true : draft.includes(p.id)}
                  onChange={(e) =>
                    setDraft((d) => (e.target.checked ? [...d, p.id] : d.filter((x) => x !== p.id)))
                  }
                />
                <span>
                  <span className="mono">{p.id}</span>
                  <br />
                  <span className="desc">{p.description}</span>
                </span>
              </label>
            ))}
          </div>
          {editable && (
            <div className="setrow">
              <button className="btn cyan btn-sm" onClick={() => void savePerms()}>
                Save permissions
              </button>
              {msg && <span className={`setmsg ${msg.ok ? "ok" : "err"}`}>{msg.text}</span>}
            </div>
          )}
        </div>
      )}
    </>
  );
}
```

- [ ] **Step 4: Chat avatars.** In the chat message type (find it in `useChatSessionStore.ts` — the interface matching the WS payload), add:

```ts
  senderId?: string;
  displayName?: string;
  avatarUrl?: string;
```

In `chat/page.tsx` (~line 65), render image avatars and display names:

```tsx
            <div className="av" style={{ background: avatarHue(m.sender) }}>
              {m.avatarUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img className="msgav" src={`${apiBase()}${m.avatarUrl}`} alt="" />
              ) : (
                m.avatar
              )}
            </div>
```

and where the sender name renders, use `{m.displayName || m.sender}`. (Import `apiBase` from `@/lib/api`.)

- [ ] **Step 5: Verify** — `npm run lint && npm run build` pass.

---

### Task 13: Full-stack verification & smoke test

**Files:** none (verification only)

- [ ] **Step 1: Backend gate** (repo root):
`podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...` → all PASS/SKIP;
`... go vet ./...` → clean.

- [ ] **Step 2: Frontend gate** (`frontend/`): `npm run lint && npm run build` → clean.

- [ ] **Step 3: Stack smoke.** Rebuild and start: `podman-compose up -d --build` (requires `.env`). Then:
  - `curl http://localhost:8080/api/v1/health` → 200.
  - `podman logs telos-core | grep -i "migration"` → migration 0004 applied; **no mock-fallback warnings** for the endpoints under test.
  - In a browser (`:8080`): log in → gear icon navigates to `/settings`.
  - Profile: set display name → chip in topbar updates; upload a PNG avatar → appears in topbar and settings; send a chat message → avatar + display name render in chat.
  - Security: change password (15+ chars) → old password rejected on re-login, other sessions gone; sessions table lists current device; revoke-others works.
  - Appearance: switch theme via settings → persists across reload and a second browser; toggle scene off → vaporwave backdrop gone; reduce motion → animations stop.
  - Admin (as Owner): create invite with role `Librarian` → accept in incognito → new user has Librarian role; toggle roles on the new user; verify own-role chips are disabled; disable the user → their session dies; delete the user → chat history shows "Deleted User"; create a custom role, edit its permissions, delete it; verify Owner role shows as immutable; verify last-Owner demotion/disable/delete is refused.
  - As a Member (non-admin): `/settings` shows no Admin sections; `curl` an admin endpoint with the member session cookie → 403.

- [ ] **Step 4: Report.** Summarize what passed/failed with command output. **Do not commit** — present the diff summary to the user and let them decide.

---

## Self-Review Notes

- **Spec coverage:** display name ✓ (T3/T10), password change + session revocation ✓ (T4/T10), avatar via ClamAV pipeline shown in settings/shell/chat ✓ (T5/T8/T9/T12), active sessions ✓ (T4/T10), server-side appearance prefs ✓ (T3/T11), invites with roles ✓ (T7/T12), user list + role assignment ✓ (T6/T12), disable/anonymize-delete with "Deleted User" attribution ✓ (T6/T12), role & permission editor with Owner immutability and builtin protection ✓ (T8/T12), gear-icon navigation ✓ (T9), Booklore-style sectioned settings layout ✓ (T9).
- **Known judgment calls for the implementer:** exact CSS token names must be verified against `tokens/*.css`; `processUploadWithLimits` refactor must keep `handleUploadFile`/`handleUploadBook` behavior identical; `VaporwaveScene` hook order; the chat store's message interface name. Where the plan says "verify X first," do it before editing.
- **Frontend note:** Next.js 16 is newer than training data — per `frontend/AGENTS.md`, consult `node_modules/next/dist/docs/` before nontrivial Next.js changes (the work here is plain client components, so this should rarely trigger).
