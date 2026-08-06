# S2 — Device-Token Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement dual-credential device token authentication and WebSocket ticket authorization for native clients while preserving `SameSite=Strict` browser cookie security.

**Architecture:** Add a new migration `0023_device_tokens.sql` for devices and hashed refresh tokens. Extend `getAuthenticatedUser` in `backend/main.go` with a Bearer branch and rescope the cookie invariant comment; implement registration, token rotation with replay detection, single-use WebSocket tickets (`Sec-WebSocket-Protocol`), and live socket revocation in `backend/devices.go`, `backend/auth.go`, and `backend/realtime.go`. Update `backend/security.go` to support `TELOS_ALLOWED_ORIGINS` for CORS and CSP, and surface device management in the Settings UI.

**Tech Stack:** Go 1.26.5 (`module telos-core`), PostgreSQL 16 (`pgx/v5`), `crypto/rand`, SHA-256 hashing, Next.js 16 / React 19 / TypeScript, Vitest, Podman.

## Global Constraints

- Go toolchain is **1.26.5**; Go is **not installed on the host**. All backend toolchain commands run in a container via **podman**, never docker.
- Frontend is **Next.js 16.2.10** static export (`output: "export"`), **React 19.2.7**, **TypeScript 5.9.3**, **Zustand 5.0.14**. Tests are **vitest 4.1.10**; E2E is **@playwright/test 1.61.1**.
- Media libraries are pinned: **epubjs 0.4.2**, **pdfjs-dist 6.1.200**, **hls.js 1.6.16**. Do not upgrade them as part of this work.
- `backend/` is one flat Go package (`module telos-core`). Extend the sibling file matching the concern; do not grow `main.go`.
- Schema changes are a **new numbered migration** in `backend/db/migrations/`. Highest existing is `0022_catalog_identity_and_progress.sql`. Migrations are idempotent (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `backend/migrations.go` verifies checksums.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. Integrate over HTTP across container boundaries only.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Never alter the environment to make a gate pass. A failing gate is reported, not forced green.
- A local backend `go build` fails unless `backend/out/` exists, until S1's build tag lands.

---

## Verification commands — use these exact forms

```bash
# Backend unit + vet (containerized)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend integration (selectors: auth|realtime|security|db|storage|product)
bash scripts/test-backend.sh auth

# Frontend (from frontend/)
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build

# Repo guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

---

## File Structure

- Create `backend/db/migrations/0023_device_tokens.sql` — database schema for device identity registry and hashed refresh tokens.
- Create `backend/devices.go` — core device token repository functions, single-use WS tickets, token generation, and DB handlers.
- Create `backend/devices_test.go` — pure unit tests for device helpers, entropy generation, SHA-256 hashing, and WS ticket store.
- Create `backend/devices_integration_test.go` — database integration tests for device registration, token rotation, replay detection, and revocation.
- Modify `backend/main.go` — update `getAuthenticatedUser` ([`backend/main.go:680-706`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L680-L706)), rescope cookie invariant comment ([`backend/main.go:685-686`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L685-L686)), update chat WS upgrade ([`backend/main.go:1275-1281`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L1275-L1281)), and register device API routes.
- Modify `backend/auth.go` — add HTTP handlers for device registration, token refresh, device listing, device revocation, and WS ticket issuance.
- Modify `backend/auth_test.go` — add integration tests for device auth HTTP handlers, token refresh replay, and cross-user device isolation.
- Modify `backend/security.go` — update `securityHeaders` ([`backend/security.go:212-236`](file:///var/home/cleadmon/Projects/Telos/backend/security.go#L212-L236)) and `devCORS` ([`backend/security.go:240-258`](file:///var/home/cleadmon/Projects/Telos/backend/security.go#L240-L258)) to support `TELOS_ALLOWED_ORIGINS`.
- Modify `backend/security_test.go` — unit tests for CORS and CSP header generation under `TELOS_ALLOWED_ORIGINS`.
- Modify `backend/event_handlers.go` — update `handleEventsWS` ([`backend/event_handlers.go:57-68`](file:///var/home/cleadmon/Projects/Telos/backend/event_handlers.go#L57-L68)) to validate single-use WS tickets from `Sec-WebSocket-Protocol`.
- Modify `backend/realtime.go` — add `DisconnectDevice(deviceID string) int` on `Hub` and device tracking on `sessionRegistry` for live socket revocation.
- Create `backend/realtime_test.go` — unit tests for `Hub.DisconnectDevice` live socket force-closure.
- Modify `frontend/src/components/settings/SecuritySection.tsx` — add Registered Devices list and revocation controls.
- Create `frontend/src/components/settings/__tests__/SecuritySectionDevices.test.tsx` — unit tests for Registered Devices UI component.

---

### Task 1: Create `0023_device_tokens.sql` migration for device identity and refresh credentials

**Files:**
- Create: `backend/db/migrations/0023_device_tokens.sql`
- Modify: `backend/migrations_test.go`
- Modify: `backend/migrations_integration_test.go`

**Interfaces:**
- Produces tables: `devices`, `device_refresh_tokens`.
- Indexes: `idx_devices_user_id`, `idx_device_refresh_tokens_device`.

- [ ] **Step 1: Write the failing migration discovery unit test**

In `backend/migrations_test.go`, add a unit test asserting that migration version 23 is discovered:

```go
func TestMigrationsContainDeviceTokens(t *testing.T) {
	ms, err := DiscoverMigrations(embeddedMigrations, "db/migrations")
	if err != nil {
		t.Fatalf("DiscoverMigrations failed: %v", err)
	}
	var found *Migration
	for _, m := range ms {
		if m.Version == 23 {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatalf("expected migration version 23 to exist, found max version %d", ms[len(ms)-1].Version)
	}
	if found.Name != "device_tokens" {
		t.Errorf("expected migration 23 name to be 'device_tokens', got %q", found.Name)
	}
}
```

- [ ] **Step 2: Run the unit test to verify failure**

Run:
```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestMigrationsContainDeviceTokens
```
Expected output: `FAIL: expected migration version 23 to exist`.

- [ ] **Step 3: Create `backend/db/migrations/0023_device_tokens.sql`**

Write the exact schema:

```sql
-- Stable device identity. Exactly one row per registered client install,
-- unchanged across token rotations.
CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name VARCHAR(128) NOT NULL,
    platform VARCHAR(64) NOT NULL,
    client_version VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

-- Rotating refresh credentials. Many rows per device over its lifetime.
-- replaced_by threads each rotation to its successor, which is what makes
-- reuse detection possible: presenting a token that already has a successor
-- means the credential leaked.
CREATE TABLE IF NOT EXISTS device_refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    replaced_by UUID REFERENCES device_refresh_tokens(id)
);

CREATE INDEX IF NOT EXISTS idx_devices_user_id ON devices(user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_device_refresh_tokens_device ON device_refresh_tokens(device_id);
```

- [ ] **Step 4: Write schema constraint integration test**

In `backend/migrations_integration_test.go`, add `TestDeviceTokensMigrationSchemaConstraints` using the standard `withFixture(t)` test helper:

```go
func TestDeviceTokensMigrationSchemaConstraints(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Negative test 1: Insert device with non-existent user_id must fail foreign key constraint
	invalidUserID := "00000000-0000-0000-0000-000000000000"
	_, err := f.DB.Exec(ctx, `
		INSERT INTO devices (user_id, device_name, platform, client_version)
		VALUES ($1, 'Test Phone', 'ios', '1.0.0')
	`, invalidUserID)
	if err == nil {
		t.Fatalf("negative test failed: expected foreign key constraint error when inserting device with invalid user_id")
	}

	// Create valid user for testing
	var validUserID string
	err = f.DB.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash)
		VALUES ('devicetestuser', 'Device Test User', 'hash')
		RETURNING id
	`).Scan(&validUserID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	// Insert valid device
	var deviceID string
	err = f.DB.QueryRow(ctx, `
		INSERT INTO devices (user_id, device_name, platform, client_version)
		VALUES ($1, 'Test Phone', 'ios', '1.0.0')
		RETURNING id
	`, validUserID).Scan(&deviceID)
	if err != nil {
		t.Fatalf("failed to insert valid device: %v", err)
	}

	// Insert refresh token
	tokenHash := "a" + strings.Repeat("0", 63)
	var refreshTokenID string
	err = f.DB.QueryRow(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
		RETURNING id
	`, deviceID, tokenHash).Scan(&refreshTokenID)
	if err != nil {
		t.Fatalf("failed to insert refresh token: %v", err)
	}

	// Negative test 2: Duplicate token_hash must violate UNIQUE constraint
	_, err = f.DB.Exec(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
	`, deviceID, tokenHash)
	if err == nil {
		t.Fatalf("negative test failed: expected UNIQUE constraint error on duplicate token_hash")
	}

	// Verify CASCADE deletion of refresh tokens when user is deleted
	_, err = f.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, validUserID)
	if err != nil {
		t.Fatalf("failed to delete test user: %v", err)
	}

	var count int
	err = f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE id = $1`, deviceID).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("expected device row to be deleted via CASCADE, got count=%d err=%v", count, err)
	}
}
```

- [ ] **Step 5: Run tests and verify success**

Run:
```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run "TestMigrationsContainDeviceTokens"
bash scripts/test-backend.sh db
```
Expected output: `PASS`.

---

### Task 2: Implement cryptographic token generation, SHA-256 hashing, and device repository functions (`backend/devices.go`)

**Files:**
- Create: `backend/devices.go`
- Create: `backend/devices_test.go`
- Create: `backend/devices_integration_test.go`

**Interfaces (binding contract):**
```go
package main

import (
	"context"
	"time"
)

type DeviceSummary struct {
	ID            string    `json:"id"`
	DeviceName    string    `json:"deviceName"`
	Platform      string    `json:"platform"`
	ClientVersion string    `json:"clientVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
}

func registerDevice(ctx context.Context, userID, deviceName, platform, clientVersion string) (deviceID, refreshToken, accessToken string, err error)
func rotateDeviceToken(ctx context.Context, presentedRefresh string) (refreshToken, accessToken string, err error)
func issueWSTicket(ctx context.Context, userID, deviceID string) (ticket string, err error)
func consumeWSTicket(ctx context.Context, ticket string) (userID, deviceID string, err error)
func revokeDevice(ctx context.Context, deviceID string) error
func listDevices(ctx context.Context, userID string) ([]DeviceSummary, error)
```

- [ ] **Step 1: Write pure unit tests in `backend/devices_test.go` including negative cases**

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestGenerateSecureTokenEntropyAndHash(t *testing.T) {
	tok, hash, err := generateSecureTokenPair()
	if err != nil {
		t.Fatalf("generateSecureTokenPair failed: %v", err)
	}
	if len(tok) < 32 {
		t.Errorf("token too short: got %d chars", len(tok))
	}
	expectedHash := sha256Hex(tok)
	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, expected %s", hash, expectedHash)
	}
}

func TestWSTicketStoreExpiryAndSingleUse(t *testing.T) {
	store := newWSTicketStore(30 * time.Second)

	ticket, err := store.Issue("user-123", "device-456")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if ticket == "" {
		t.Fatal("expected non-empty ticket")
	}

	// Valid consumption
	userID, deviceID, err := store.Consume(ticket)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if userID != "user-123" || deviceID != "device-456" {
		t.Fatalf("unexpected stored credentials: user=%s device=%s", userID, deviceID)
	}

	// Negative test 1: Replayed/already-consumed ticket must return error
	_, _, err = store.Consume(ticket)
	if err == nil {
		t.Fatalf("negative test failed: expected error on replayed ticket consumption")
	}

	// Negative test 2: Non-existent ticket must return error
	_, _, err = store.Consume("invalid-ticket-value")
	if err == nil {
		t.Fatalf("negative test failed: expected error on non-existent ticket")
	}

	// Negative test 3: Expired ticket must return error
	shortStore := newWSTicketStore(-1 * time.Second)
	expTicket, _ := shortStore.Issue("user-789", "device-999")
	_, _, err = shortStore.Consume(expTicket)
	if err == nil {
		t.Fatalf("negative test failed: expected error on expired ticket")
	}
}
```

- [ ] **Step 2: Write integration tests in `backend/devices_integration_test.go` using `withFixture(t)`**

```go
package main

import (
	"context"
	"testing"
)

func TestRegisterDeviceAndRotateToken(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('devuser','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Register device
	deviceID, refresh1, access1, err := registerDevice(ctx, userID, "Desktop App", "linux", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}
	if deviceID == "" || refresh1 == "" || access1 == "" {
		t.Fatalf("invalid tokens returned from registerDevice")
	}

	// Verify device in DB
	devs, err := listDevices(ctx, userID)
	if err != nil || len(devs) != 1 {
		t.Fatalf("listDevices returned %d devices, err=%v", len(devs), err)
	}
	if devs[0].ID != deviceID || devs[0].DeviceName != "Desktop App" {
		t.Fatalf("device summary mismatch: %+v", devs[0])
	}

	// Rotate refresh token
	refresh2, access2, err := rotateDeviceToken(ctx, refresh1)
	if err != nil {
		t.Fatalf("rotateDeviceToken failed: %v", err)
	}
	if refresh2 == refresh1 || access2 == access1 {
		t.Fatalf("token rotation did not generate new tokens")
	}

	// Replay Detection: presenting refresh1 again must fail and revoke the device
	_, _, err = rotateDeviceToken(ctx, refresh1)
	if err == nil {
		t.Fatalf("negative test failed: replayed refresh token was accepted")
	}

	// Verify device is revoked after replay attack
	devsAfter, err := listDevices(ctx, userID)
	if err != nil || len(devsAfter) != 0 {
		t.Fatalf("expected 0 active devices after replay detection revocation, got %d", len(devsAfter))
	}
}
```

- [ ] **Step 3: Run unit and integration tests to confirm failure before implementation**

Run:
```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run "TestGenerateSecureTokenEntropyAndHash|TestWSTicketStoreExpiryAndSingleUse"
```
Expected output: `FAIL` (undefined functions).

- [ ] **Step 4: Create `backend/devices.go`**

```go
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type DeviceSummary struct {
	ID            string    `json:"id"`
	DeviceName    string    `json:"deviceName"`
	Platform      string    `json:"platform"`
	ClientVersion string    `json:"clientVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
}

type wsTicketEntry struct {
	userID    string
	deviceID  string
	expiresAt time.Time
}

type wsTicketStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]wsTicketEntry
}

var globalWSTicketStore = newWSTicketStore(30 * time.Second)

func newWSTicketStore(ttl time.Duration) *wsTicketStore {
	return &wsTicketStore{
		ttl:     ttl,
		tickets: make(map[string]wsTicketEntry),
	}
}

func (s *wsTicketStore) Issue(userID, deviceID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate ticket entropy: %w", err)
	}
	ticket := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[ticket] = wsTicketEntry{
		userID:    userID,
		deviceID:  deviceID,
		expiresAt: time.Now().Add(s.ttl),
	}
	return ticket, nil
}

func (s *wsTicketStore) Consume(ticket string) (userID, deviceID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tickets[ticket]
	if !ok {
		return "", "", errors.New("invalid or consumed ticket")
	}
	delete(s.tickets, ticket)

	if time.Now().After(entry.expiresAt) {
		return "", "", errors.New("expired ticket")
	}
	return entry.userID, entry.deviceID, nil
}

func generateSecureTokenPair() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("entropy failure: %w", err)
	}
	token = hex.EncodeToString(b)
	hash = sha256Hex(token)
	return token, hash, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func registerDevice(ctx context.Context, userID, deviceName, platform, clientVersion string) (deviceID, refreshToken, accessToken string, err error) {
	if dbPool == nil {
		return "", "", "", errors.New("db uninitialized")
	}
	if userID == "" || deviceName == "" || platform == "" || clientVersion == "" {
		return "", "", "", errors.New("missing required device registration parameters")
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO devices (user_id, device_name, platform, client_version, last_seen_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id
	`, userID, deviceName, platform, clientVersion).Scan(&deviceID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert device: %w", err)
	}

	rawRefresh, refreshHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", "", err
	}

	rawAccess, accessHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", "", err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
	`, deviceID, refreshHash)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert refresh token: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, client_ip, created_at, expires_at)
		VALUES ($1, $2, $3, '0.0.0.0', NOW(), NOW() + INTERVAL '15 minutes')
	`, userID, accessHash, fmt.Sprintf("%s/%s (%s)", deviceName, clientVersion, platform))
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert access token session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", err
	}

	return deviceID, rawRefresh, rawAccess, nil
}

func rotateDeviceToken(ctx context.Context, presentedRefresh string) (refreshToken, accessToken string, err error) {
	if dbPool == nil {
		return "", "", errors.New("db uninitialized")
	}
	presentedHash := sha256Hex(presentedRefresh)

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	var tokenID, deviceID, userID string
	var expiresAt time.Time
	var consumedAt *time.Time
	var replacedBy *string
	var revokedAt *time.Time

	err = tx.QueryRow(ctx, `
		SELECT rt.id, rt.device_id, d.user_id, rt.expires_at, rt.consumed_at, rt.replaced_by, d.revoked_at
		FROM device_refresh_tokens rt
		JOIN devices d ON d.id = rt.device_id
		WHERE rt.token_hash = $1
		FOR UPDATE OF rt
	`, presentedHash).Scan(&tokenID, &deviceID, &userID, &expiresAt, &consumedAt, &replacedBy, &revokedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("invalid refresh token")
		}
		return "", "", err
	}

	if revokedAt != nil {
		return "", "", errors.New("device revoked")
	}

	if consumedAt != nil || replacedBy != nil {
		_, _ = tx.Exec(ctx, `UPDATE devices SET revoked_at = NOW() WHERE id = $1`, deviceID)
		_ = tx.Commit(ctx)

		if hubInstance != nil {
			hubInstance.DisconnectDevice(deviceID)
		}
		return "", "", errors.New("refresh token reuse detected; device revoked")
	}

	if time.Now().After(expiresAt) {
		return "", "", errors.New("refresh token expired")
	}

	newRawRefresh, newRefreshHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", err
	}

	newRawAccess, newAccessHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", err
	}

	var successorID string
	err = tx.QueryRow(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
		RETURNING id
	`, deviceID, newRefreshHash).Scan(&successorID)
	if err != nil {
		return "", "", err
	}

	_, err = tx.Exec(ctx, `
		UPDATE device_refresh_tokens
		SET consumed_at = NOW(), replaced_by = $1
		WHERE id = $2
	`, successorID, tokenID)
	if err != nil {
		return "", "", err
	}

	_, _ = tx.Exec(ctx, `UPDATE devices SET last_seen_at = NOW() WHERE id = $1`, deviceID)

	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, client_ip, created_at, expires_at)
		VALUES ($1, $2, 'TelosDevice/1.0', '0.0.0.0', NOW(), NOW() + INTERVAL '15 minutes')
	`, userID, newAccessHash)
	if err != nil {
		return "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}

	return newRawRefresh, newRawAccess, nil
}

func issueWSTicket(ctx context.Context, userID, deviceID string) (ticket string, err error) {
	if userID == "" {
		return "", errors.New("unauthorized: missing user identity")
	}
	return globalWSTicketStore.Issue(userID, deviceID)
}

func consumeWSTicket(ctx context.Context, ticket string) (userID, deviceID string, err error) {
	return globalWSTicketStore.Consume(ticket)
}

func revokeDevice(ctx context.Context, deviceID string) error {
	if dbPool == nil {
		return errors.New("db uninitialized")
	}
	res, err := dbPool.Exec(ctx, `
		UPDATE devices SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL
	`, deviceID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return errors.New("device not found or already revoked")
	}
	if hubInstance != nil {
		hubInstance.DisconnectDevice(deviceID)
	}
	return nil
}

func listDevices(ctx context.Context, userID string) ([]DeviceSummary, error) {
	if dbPool == nil {
		return nil, errors.New("db uninitialized")
	}
	rows, err := dbPool.Query(ctx, `
		SELECT id, device_name, platform, client_version, created_at, last_seen_at
		FROM devices
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DeviceSummary
	for rows.Next() {
		var d DeviceSummary
		if err := rows.Scan(&d.ID, &d.DeviceName, &d.Platform, &d.ClientVersion, &d.CreatedAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if result == nil {
		result = []DeviceSummary{}
	}
	return result, nil
}
```

- [ ] **Step 5: Run tests and verify success**

Run:
```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run "TestGenerateSecureTokenEntropyAndHash|TestWSTicketStoreExpiryAndSingleUse"
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 3: Implement device registration endpoint with IP rate limiting and audit logging (`POST /api/v1/auth/devices/register`)

**Files:**
- Modify: `backend/auth.go`
- Modify: `backend/main.go`
- Modify: `backend/auth_test.go`

**Interfaces:**
- Route: `POST /api/v1/auth/devices/register`
- Request JSON: `{ "deviceName": "...", "platform": "...", "clientVersion": "..." }`
- Response JSON: `{ "deviceId": "...", "refreshToken": "...", "accessToken": "...", "accessExpiresIn": 900 }`

- [ ] **Step 1: Write HTTP integration tests in `backend/auth_test.go` using `withFixture(t)`**

```go
func TestDeviceRegistrationEndpoint(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed test user and active session cookie token
	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('reguser','x') RETURNING id`).Scan(&userID)
	rawSessionToken := "test-session-token-reg-123"
	sessionHash := sha256Hex(rawSessionToken)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sessionHash, userID)

	// Positive test: Register device with valid cookie
	body := `{"deviceName":"My Phone","platform":"android","clientVersion":"2.1.0"}`
	r := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSessionToken})
	rec := httptest.NewRecorder()

	handleRegisterDevice(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRegisterDevice returned code %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		DeviceID        string `json:"deviceId"`
		RefreshToken    string `json:"refreshToken"`
		AccessToken     string `json:"accessToken"`
		AccessExpiresIn int    `json:"accessExpiresIn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.DeviceID == "" || res.RefreshToken == "" || res.AccessToken == "" {
		t.Fatalf("expected non-empty tokens and deviceId, got %+v", res)
	}
	if res.AccessExpiresIn != 900 {
		t.Errorf("expected accessExpiresIn to be 900, got %d", res.AccessExpiresIn)
	}

	// Negative test 1: Unauthenticated request (no session token)
	rUnauth := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(body))
	rUnauth.Header.Set("Content-Type", "application/json")
	recUnauth := httptest.NewRecorder()
	handleRegisterDevice(recUnauth, rUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("negative test failed: expected 401 Unauthorized for unauthenticated registration, got %d", recUnauth.Code)
	}

	// Negative test 2: Missing required payload fields
	badBody := `{"deviceName":"","platform":"android"}`
	rBad := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(badBody))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSessionToken})
	recBad := httptest.NewRecorder()
	handleRegisterDevice(recBad, rBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("negative test failed: expected 400 Bad Request for missing fields, got %d", recBad.Code)
	}
}
```

- [ ] **Step 2: Add `handleRegisterDevice` handler to `backend/auth.go`**

```go
func handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	uc, err := getAuthenticatedUser(r)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required to register device")
		return
	}

	var req struct {
		DeviceName    string `json:"deviceName"`
		Platform      string `json:"platform"`
		ClientVersion string `json:"clientVersion"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "Malformed request body")
		return
	}

	if req.DeviceName == "" || req.Platform == "" || req.ClientVersion == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_parameters", "deviceName, platform, and clientVersion are required")
		return
	}

	deviceID, refreshToken, accessToken, err := registerDevice(r.Context(), uc.ID, req.DeviceName, req.Platform, req.ClientVersion)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "registration_failed", "Failed to register device")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deviceId":        deviceID,
		"refreshToken":    refreshToken,
		"accessToken":     accessToken,
		"accessExpiresIn": 900,
	})
}
```

- [ ] **Step 3: Register route in `backend/main.go`**

In `backend/main.go`, add route registration inside `main()`:
```go
mux.HandleFunc("/api/v1/auth/devices/register", handleRegisterDevice)
```

- [ ] **Step 4: Run integration tests and verify pass**

Run:
```bash
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 4: Implement token refresh with single-use rotation and reuse replay detection (`POST /api/v1/auth/devices/refresh`)

**Files:**
- Modify: `backend/auth.go`
- Modify: `backend/main.go`
- Modify: `backend/auth_test.go`

**Interfaces:**
- Route: `POST /api/v1/auth/devices/refresh`
- Request JSON: `{ "refreshToken": "..." }`
- Response JSON: `{ "refreshToken": "...", "accessToken": "...", "accessExpiresIn": 900 }`

- [ ] **Step 1: Write integration tests in `backend/auth_test.go` using `withFixture(t)`**

```go
func TestDeviceRefreshEndpointRotationAndReplay(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('refuser','x') RETURNING id`).Scan(&userID)
	deviceID, refresh1, _, err := registerDevice(ctx, userID, "Tablet", "android", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	// Positive test: Refresh token rotation
	body := fmt.Sprintf(`{"refreshToken":%q}`, refresh1)
	r := httptest.NewRequest("POST", "/api/v1/auth/devices/refresh", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleRefreshDevice(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRefreshDevice returned code %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		RefreshToken    string `json:"refreshToken"`
		AccessToken     string `json:"accessToken"`
		AccessExpiresIn int    `json:"accessExpiresIn"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.RefreshToken == "" || res.RefreshToken == refresh1 {
		t.Fatalf("expected new rotated refresh token, got %q", res.RefreshToken)
	}

	// Negative test 1: Replayed refresh token MUST return 401 and trigger device revocation
	rReplay := httptest.NewRequest("POST", "/api/v1/auth/devices/refresh", strings.NewReader(body))
	rReplay.Header.Set("Content-Type", "application/json")
	recReplay := httptest.NewRecorder()

	handleRefreshDevice(recReplay, rReplay)

	if recReplay.Code != http.StatusUnauthorized {
		t.Fatalf("negative test failed: expected 401 Unauthorized for replayed refresh token, got %d", recReplay.Code)
	}

	// Verify device is now revoked in database
	var revokedAt *time.Time
	f.DB.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceID).Scan(&revokedAt)
	if revokedAt == nil {
		t.Fatalf("negative test failed: device was not revoked after refresh token replay attack")
	}
}
```

- [ ] **Step 2: Add `handleRefreshDevice` handler to `backend/auth.go`**

```go
func handleRefreshDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "Malformed request body")
		return
	}

	if req.RefreshToken == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_parameters", "refreshToken is required")
		return
	}

	newRefresh, newAccess, err := rotateDeviceToken(r.Context(), req.RefreshToken)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"refreshToken":    newRefresh,
		"accessToken":     newAccess,
		"accessExpiresIn": 900,
	})
}
```

- [ ] **Step 3: Register route in `backend/main.go`**

In `backend/main.go`, add route registration in `main()`:
```go
mux.HandleFunc("/api/v1/auth/devices/refresh", handleRefreshDevice)
```

- [ ] **Step 4: Run integration tests and verify pass**

Run:
```bash
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 5: Add Bearer branch in `getAuthenticatedUser` and update cookie invariant comment (`backend/main.go`)

**Files:**
- Modify: `backend/main.go` (cite [`backend/main.go:680-706`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L680-L706), [`backend/main.go:685-686`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L685-L686))
- Modify: `backend/auth_test.go`

**Interfaces:**
- Updates `getAuthenticatedUser(r *http.Request) (*UserContext, error)` signature unchanged, functionality expanded to inspect `Authorization: Bearer <token>`.

- [ ] **Step 1: Write tests in `backend/auth_test.go` for Bearer token authentication**

```go
func TestGetAuthenticatedUserBearerBranch(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('beareruser','x') RETURNING id`).Scan(&userID)

	rawBearerToken := "test-bearer-token-val-456"
	bearerHash := sha256Hex(rawBearerToken)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')`, bearerHash, userID)

	// Positive test: Bearer header authentication
	rBearer := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rBearer.Header.Set("Authorization", "Bearer "+rawBearerToken)
	uc, err := getAuthenticatedUser(rBearer)
	if err != nil {
		t.Fatalf("getAuthenticatedUser failed for valid Bearer token: %v", err)
	}
	if uc.ID != userID {
		t.Fatalf("user ID mismatch: got %s, want %s", uc.ID, userID)
	}

	// Negative test 1: Expired bearer token
	rawExpToken := "test-expired-bearer-token-789"
	expHash := sha256Hex(rawExpToken)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() - INTERVAL '1 minute')`, expHash, userID)
	rExp := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rExp.Header.Set("Authorization", "Bearer "+rawExpToken)
	_, err = getAuthenticatedUser(rExp)
	if err == nil {
		t.Fatalf("negative test failed: expected error for expired bearer token")
	}

	// Negative test 2: Malformed Bearer header
	rBad := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rBad.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	_, err = getAuthenticatedUser(rBad)
	if err == nil {
		t.Fatalf("negative test failed: expected error for malformed auth header")
	}
}
```

- [ ] **Step 2: Update `getAuthenticatedUser` and invariant comment in `backend/main.go:680-706`**

Replace `getAuthenticatedUser` at [`backend/main.go:680-706`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L680-L706):

```go
func getAuthenticatedUser(r *http.Request) (*UserContext, error) {
	if dbPool == nil {
		return nil, errors.New("db uninitialized")
	}

	// Dual-credential model: Browser cookie sessions remain HttpOnly and exact-origin
	// checked. Native client sessions authenticate via Authorization: Bearer <token>.
	// Tokens are never accepted from query string or fragment.
	token, err := sessionTokenFromRequest(r)
	if err != nil {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if token == "" {
		return nil, errors.New("missing authentication token")
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	// Single aggregate query for identity + roles + sorted permissions.
	uc, err := LoadAuthenticatedUser(r.Context(), dbPool, tokenHash)
	if err != nil {
		return nil, err
	}

	// Coalesced, bounded last-seen stamp — never blocks or spawns a goroutine.
	if sessionTouches != nil {
		sessionTouches.Touch(tokenHash)
	}
	return uc, nil
}
```

- [ ] **Step 3: Run integration test to verify success**

Run:
```bash
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 6: Implement device listing and revocation endpoints with strict cross-user authorization (`GET /api/v1/users/me/devices`, `DELETE /api/v1/users/me/devices/{id}`)

**Files:**
- Modify: `backend/auth.go`
- Modify: `backend/main.go`
- Modify: `backend/auth_test.go`

**Interfaces:**
- `GET /api/v1/users/me/devices` -> returns `DeviceSummary[]`
- `DELETE /api/v1/users/me/devices/{id}` -> returns HTTP 204

- [ ] **Step 1: Write integration tests in `backend/auth_test.go` enforcing Defect 2 cross-user isolation guarantees**

```go
func TestListAndRevokeDevicesEndpoint(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('user1','x') RETURNING id`).Scan(&userID)
	devID, _, _, _ := registerDevice(ctx, userID, "Phone", "ios", "1.0")

	rawSess := "sess-user1-list-revoke"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sha256Hex(rawSess), userID)

	// GET /api/v1/users/me/devices
	rList := httptest.NewRequest("GET", "/api/v1/users/me/devices", nil)
	rList.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSess})
	recList := httptest.NewRecorder()
	handleListDevices(recList, rList)

	if recList.Code != http.StatusOK {
		t.Fatalf("handleListDevices returned code %d, want 200", recList.Code)
	}

	var devs []DeviceSummary
	json.Unmarshal(recList.Body.Bytes(), &devs)
	if len(devs) != 1 || devs[0].ID != devID {
		t.Fatalf("unexpected devices list: %+v", devs)
	}

	// DELETE /api/v1/users/me/devices/{id}
	rDel := httptest.NewRequest("DELETE", "/api/v1/users/me/devices/"+devID, nil)
	rDel.SetPathValue("id", devID)
	rDel.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSess})
	recDel := httptest.NewRecorder()
	handleRevokeDevice(recDel, rDel)

	if recDel.Code != http.StatusNoContent {
		t.Fatalf("handleRevokeDevice returned code %d, want 204", recDel.Code)
	}
}

func TestCrossUserDeviceAuthorization(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed User A and User B
	var userA, userB string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('usera','x') RETURNING id`).Scan(&userA)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('userb','x') RETURNING id`).Scan(&userB)

	// User B registers a device
	deviceB, _, _, _ := registerDevice(ctx, userB, "UserB Laptop", "macos", "1.0")

	// User A session token
	sessA := "sess-user-a-token-999"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sha256Hex(sessA), userA)

	// Requirement 1: listDevices for user A NEVER returns user B's device
	rListA := httptest.NewRequest("GET", "/api/v1/users/me/devices", nil)
	rListA.AddCookie(&http.Cookie{Name: "telos_session", Value: sessA})
	recListA := httptest.NewRecorder()
	handleListDevices(recListA, rListA)

	var devsA []DeviceSummary
	json.Unmarshal(recListA.Body.Bytes(), &devsA)
	for _, d := range devsA {
		if d.ID == deviceB {
			t.Fatalf("SECURITY FAILURE: listDevices for User A returned User B's device %s", deviceB)
		}
	}

	// Requirement 2: DELETE /api/v1/users/me/devices/{id} with User A's credentials and User B's device ID returns 404 (NOT 403)
	rDelCross := httptest.NewRequest("DELETE", "/api/v1/users/me/devices/"+deviceB, nil)
	rDelCross.SetPathValue("id", deviceB)
	rDelCross.AddCookie(&http.Cookie{Name: "telos_session", Value: sessA})
	recDelCross := httptest.NewRecorder()
	handleRevokeDevice(recDelCross, rDelCross)

	if recDelCross.Code != http.StatusNotFound {
		t.Fatalf("SECURITY FAILURE: cross-user device revocation returned code %d, want 404 Not Found to prevent registry leakage", recDelCross.Code)
	}

	// Requirement 3: After that attempt, User B's device is STILL live and usable (revoked_at IS NULL)
	var revokedAt *time.Time
	f.DB.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceB).Scan(&revokedAt)
	if revokedAt != nil {
		t.Fatalf("SECURITY FAILURE: User B's device was revoked by User A's unauthorized request")
	}
}
```

- [ ] **Step 2: Add `handleListDevices` and `handleRevokeDevice` handlers in `backend/auth.go`**

```go
func handleListDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}

	uc, err := getAuthenticatedUser(r)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	devs, err := listDevices(r.Context(), uc.ID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db_error", "Failed to list devices")
		return
	}

	writeJSON(w, http.StatusOK, devs)
}

func handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required")
		return
	}

	uc, err := getAuthenticatedUser(r)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	targetID := r.PathValue("id")
	if targetID == "" {
		targetID = strings.TrimPrefix(r.URL.Path, "/api/v1/users/me/devices/")
	}
	if targetID == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Device ID is required")
		return
	}

	// Verify target device belongs to the requesting user (returns 404 on mismatch to prevent registry leakage)
	var deviceOwnerID string
	err = dbPool.QueryRow(r.Context(), `SELECT user_id FROM devices WHERE id = $1 AND revoked_at IS NULL`, targetID).Scan(&deviceOwnerID)
	if err != nil || deviceOwnerID != uc.ID {
		writeAPIError(w, r, http.StatusNotFound, "device_not_found", "Device not found")
		return
	}

	if err := revokeDevice(r.Context(), targetID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "revocation_failed", "Failed to revoke device")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Register HTTP routes in `backend/main.go`**

In `backend/main.go`:
```go
mux.HandleFunc("GET /api/v1/users/me/devices", handleListDevices)
mux.HandleFunc("DELETE /api/v1/users/me/devices/{id}", handleRevokeDevice)
```

- [ ] **Step 4: Run integration tests and verify pass**

Run:
```bash
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 7: Implement single-use WebSocket ticket issuance and `Sec-WebSocket-Protocol` upgrade validation

**Files:**
- Modify: `backend/auth.go`
- Modify: `backend/main.go` (cite [`backend/main.go:1275-1281`](file:///var/home/cleadmon/Projects/Telos/backend/main.go#L1275-L1281))
- Modify: `backend/event_handlers.go` (cite [`backend/event_handlers.go:57-68`](file:///var/home/cleadmon/Projects/Telos/backend/event_handlers.go#L57-L68))
- Modify: `backend/auth_test.go`

**Interfaces:**
- Endpoint: `POST /api/v1/auth/ws-ticket` -> `{ "ticket": "...", "expiresIn": 30 }`
- WebSocket subprotocol header parsing: `Sec-WebSocket-Protocol: telos-ticket.<ticket>`

- [ ] **Step 1: Write integration tests in `backend/auth_test.go` for WS tickets including cross-user ticket isolation**

```go
func TestWSTicketIssuanceAndUpgrade(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsticketuser','x') RETURNING id`).Scan(&userID)
	deviceID, _, _, _ := registerDevice(ctx, userID, "PC", "linux", "1.0")

	rawBearer := "bearer-ws-ticket-test-123"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')`, sha256Hex(rawBearer), userID)

	// POST /api/v1/auth/ws-ticket
	rTicket := httptest.NewRequest("POST", "/api/v1/auth/ws-ticket", nil)
	rTicket.Header.Set("Authorization", "Bearer "+rawBearer)
	recTicket := httptest.NewRecorder()

	handleWSTicket(recTicket, rTicket)

	if recTicket.Code != http.StatusOK {
		t.Fatalf("handleWSTicket returned code %d, want 200", recTicket.Code)
	}

	var res struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	json.Unmarshal(recTicket.Body.Bytes(), &res)
	if res.Ticket == "" || res.ExpiresIn != 30 {
		t.Fatalf("invalid ticket response: %+v", res)
	}

	// Consume ticket
	uID, dID, err := consumeWSTicket(ctx, res.Ticket)
	if err != nil || uID != userID {
		t.Fatalf("consumeWSTicket failed: got user=%s err=%v", uID, err)
	}
}

func TestCrossUserWSTicketIsolation(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userA, userB string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsusera','x') RETURNING id`).Scan(&userA)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsuserb','x') RETURNING id`).Scan(&userB)

	devA, _, _, _ := registerDevice(ctx, userA, "UserA Phone", "ios", "1.0")

	// Issue WS ticket for User A
	ticketA, err := issueWSTicket(ctx, userA, devA)
	if err != nil {
		t.Fatalf("issueWSTicket failed: %v", err)
	}

	// Consume ticket
	claimedUser, claimedDev, err := consumeWSTicket(ctx, ticketA)
	if err != nil {
		t.Fatalf("consumeWSTicket failed: %v", err)
	}

	// Requirement: Ticket issued for User A MUST NOT yield User B identity
	if claimedUser == userB {
		t.Fatalf("SECURITY FAILURE: WS ticket issued to User A resolved to User B identity")
	}
	if claimedUser != userA || claimedDev != devA {
		t.Fatalf("unexpected ticket resolution: got user=%s dev=%s, want userA=%s devA=%s", claimedUser, claimedDev, userA, devA)
	}
}
```

- [ ] **Step 2: Add `handleWSTicket` handler to `backend/auth.go`**

```go
func handleWSTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	uc, err := getAuthenticatedUser(r)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	ticket, err := issueWSTicket(r.Context(), uc.ID, "")
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "ticket_error", "Failed to issue WS ticket")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ticket":    ticket,
		"expiresIn": 30,
	})
}
```

- [ ] **Step 3: Update Chat WebSocket upgrade in `backend/main.go:1275-1281`**

In `backend/main.go`:
```go
func parseWSTicketHeader(r *http.Request) string {
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	for _, part := range strings.Split(proto, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "telos-ticket.") {
			return strings.TrimPrefix(part, "telos-ticket.")
		}
	}
	return ""
}
```

- [ ] **Step 4: Update Events WebSocket upgrade in `backend/event_handlers.go:57-68`**

In `backend/event_handlers.go`, update ticket extraction from `Sec-WebSocket-Protocol` header prior to `upgrader.Upgrade`.

- [ ] **Step 5: Register HTTP route in `backend/main.go`**

```go
mux.HandleFunc("/api/v1/auth/ws-ticket", handleWSTicket)
```

- [ ] **Step 6: Run integration tests and verify pass**

Run:
```bash
bash scripts/test-backend.sh auth
```
Expected output: `PASS`.

---

### Task 8: Implement live WebSocket termination on device revocation (`backend/realtime.go`)

**Files:**
- Modify: `backend/realtime.go`
- Create: `backend/realtime_test.go`

**Interfaces:**
- Signature: `func (h *Hub) DisconnectDevice(deviceID string) int`

- [ ] **Step 1: Write unit tests in `backend/realtime_test.go` for live device disconnection**

```go
package main

import (
	"testing"
)

type mockRevocableConn struct {
	closed bool
	code   int
}

func (m *mockRevocableConn) CloseWithCode(code int, reason string) {
	m.closed = true
	m.code = code
}

func TestHubDisconnectDeviceClosesSockets(t *testing.T) {
	reg := newSessionRegistry()
	conn1 := &mockRevocableConn{}
	conn2 := &mockRevocableConn{}

	release1, ok1 := reg.RegisterDevice("sess-1", "user-1", "dev-A", conn1)
	release2, ok2 := reg.RegisterDevice("sess-2", "user-1", "dev-B", conn2)
	if !ok1 || !ok2 {
		t.Fatalf("failed to register mock connections")
	}
	defer release1()
	defer release2()

	count := reg.RevokeDevice("dev-A")
	if count != 1 {
		t.Fatalf("RevokeDevice returned count %d, want 1", count)
	}
	if !conn1.closed || conn1.code != 4001 {
		t.Fatalf("expected conn1 to be closed with code 4001, got closed=%v code=%d", conn1.closed, conn1.code)
	}
	if conn2.closed {
		t.Fatalf("conn2 for dev-B was closed unexpectedly")
	}
}
```

- [ ] **Step 2: Update `sessionRegistry` and implement `DisconnectDevice` in `backend/realtime.go`**

Add `deviceID` tracking to `sessionRegistry` in `backend/realtime.go`:

```go
type registeredConn struct {
	sessionHash string
	userID      string
	deviceID    string
	conn        RevocableConnection
}

type Hub struct{}

var hubInstance = &Hub{}

func (h *Hub) DisconnectDevice(deviceID string) int {
	if sessionRegistryInstance == nil {
		return 0
	}
	return sessionRegistryInstance.RevokeDevice(deviceID)
}

func (r *sessionRegistry) RegisterDevice(sessionHash, userID, deviceID string, conn RevocableConnection) (func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.perUser[userID] >= maxSocketsPerUser {
		return nil, false
	}
	rc := &registeredConn{sessionHash: sessionHash, userID: userID, deviceID: deviceID, conn: conn}
	if r.bySess[sessionHash] == nil {
		r.bySess[sessionHash] = make(map[*registeredConn]struct{})
	}
	r.bySess[sessionHash][rc] = struct{}{}
	if r.byUser[userID] == nil {
		r.byUser[userID] = make(map[*registeredConn]struct{})
	}
	r.byUser[userID][rc] = struct{}{}
	r.perUser[userID]++

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.removeLocked(rc)
	}, true
}

func (r *sessionRegistry) RevokeDevice(deviceID string) int {
	if deviceID == "" {
		return 0
	}
	r.mu.Lock()
	conns := make([]RevocableConnection, 0)
	for _, set := range r.byUser {
		for rc := range set {
			if rc.deviceID == deviceID {
				conns = append(conns, rc.conn)
			}
		}
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.CloseWithCode(4001, "Device Revoked")
	}
	return len(conns)
}
```

- [ ] **Step 3: Run unit test to verify pass**

Run:
```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestHubDisconnectDeviceClosesSockets
```
Expected output: `PASS`.

---

### Task 9: Implement configurable CORS and CSP policy via `TELOS_ALLOWED_ORIGINS` (`backend/security.go`)

**Files:**
- Modify: `backend/security.go` (cite [`backend/security.go:212-236`](file:///var/home/cleadmon/Projects/Telos/backend/security.go#L212-L236), [`backend/security.go:240-258`](file:///var/home/cleadmon/Projects/Telos/backend/security.go#L240-L258))
- Modify: `backend/security_test.go`

- [ ] **Step 1: Write unit tests in `backend/security_test.go` for allowed origins**

```go
func TestAllowedOriginsCORSAndCSP(t *testing.T) {
	t.Setenv("TELOS_ALLOWED_ORIGINS", "https://client.example.com,tauri://localhost")

	cfg := loadSecurityConfig()

	// Verify CORS header generation for allowed origin
	req := httptest.NewRequest("OPTIONS", "/api/v1/auth/devices/register", nil)
	req.Header.Set("Origin", "https://client.example.com")
	rec := httptest.NewRecorder()

	handler := devCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), cfg)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://client.example.com" {
		t.Fatalf("expected CORS allow origin header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// Verify CSP connect-src contains allowed origins
	secHandler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), cfg)
	recSec := httptest.NewRecorder()
	secHandler.ServeHTTP(recSec, httptest.NewRequest("GET", "/", nil))

	csp := recSec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://client.example.com") || !strings.Contains(csp, "tauri://localhost") {
		t.Fatalf("Content-Security-Policy connect-src missing allowed origins: %s", csp)
	}
}
```

- [ ] **Step 2: Update `devCORS` and `securityHeaders` in `backend/security.go:212-258`**

Update `security.go` to inspect `TELOS_ALLOWED_ORIGINS`:

```go
func parseAllowedOrigins() []string {
	raw := os.Getenv("TELOS_ALLOWED_ORIGINS")
	if raw == "" {
		return nil
	}
	var origins []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			origins = append(origins, s)
		}
	}
	return origins
}
```

Update `devCORS` at [`backend/security.go:240-258`](file:///var/home/cleadmon/Projects/Telos/backend/security.go#L240-L258) to permit allowed origins in any environment when `TELOS_ALLOWED_ORIGINS` is configured.

- [ ] **Step 3: Run integration test suite for security**

Run:
```bash
bash scripts/test-backend.sh security
```
Expected output: `PASS`.

---

### Task 10: Build Settings Registered Devices UI (`frontend/src/components/settings/SecuritySection.tsx`)

**Files:**
- Modify: `frontend/src/components/settings/SecuritySection.tsx`
- Create: `frontend/src/components/settings/__tests__/SecuritySectionDevices.test.tsx`

- [ ] **Step 1: Write Vitest component test for Registered Devices UI**

Create `frontend/src/components/settings/__tests__/SecuritySectionDevices.test.tsx`:

```tsx
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { SecuritySection } from "../SecuritySection";

describe("SecuritySection Registered Devices", () => {
  it("renders registered devices list and handles revocation", async () => {
    const mockDevices = [
      {
        id: "dev-1",
        deviceName: "My iPhone",
        platform: "ios",
        clientVersion: "1.0.0",
        createdAt: "2026-08-01T00:00:00Z",
        lastSeenAt: "2026-08-06T12:00:00Z",
      },
    ];

    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      if (url.endsWith("/api/v1/users/me/devices") && (!init || init.method === "GET")) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(mockDevices),
        });
      }
      if (url.endsWith("/api/v1/users/me/devices/dev-1") && init?.method === "DELETE") {
        return Promise.resolve({ ok: true, status: 204 });
      }
      return Promise.resolve({ ok: false, status: 404 });
    }));

    render(<SecuritySection />);

    await waitFor(() => {
      expect(screen.getByText("My iPhone")).toBeInTheDocument();
    });

    const revokeBtn = screen.getByRole("button", { name: /revoke/i });
    fireEvent.click(revokeBtn);

    await waitFor(() => {
      expect(screen.queryByText("My iPhone")).not.toBeInTheDocument();
    });
  });
});
```

- [ ] **Step 2: Add Registered Devices component to `SecuritySection.tsx`**

In `frontend/src/components/settings/SecuritySection.tsx`, fetch `DeviceSummary[]` from `/api/v1/users/me/devices` and display a management table/list with revocation action buttons.

- [ ] **Step 3: Run frontend unit tests and linting**

Run:
```bash
cd frontend && npm run lint && npx tsc --noEmit && npm run test:unit
```
Expected output: `PASS`.
