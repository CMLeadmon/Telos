package main

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505), e.g. a duplicate username.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// sha256Hex returns the lowercase hex SHA-256 of s (used for token hashing).
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// Username canonicalization
// ---------------------------------------------------------------------------

var canonicalUsernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

// canonicalUsername lowercases ASCII letters and requires the result to match
// [a-z0-9][a-z0-9_.-]{2,31}. Any leading/trailing/embedded/Unicode whitespace,
// control character, or non-ASCII rune is rejected rather than trimmed. The
// display name remains the Unicode-facing field.
func canonicalUsername(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("username is required")
	}
	for _, r := range raw {
		if r > 0x7f {
			return "", errors.New("username must be ASCII")
		}
	}
	// ASCII lowercase only (no Unicode folding, which could introduce
	// ambiguity); the regex then enforces the exact allowed set.
	lower := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, raw)
	if !canonicalUsernameRE.MatchString(lower) {
		return "", errors.New("username must be 3-32 chars of a-z 0-9 . _ - and start alphanumeric")
	}
	return lower, nil
}

// ---------------------------------------------------------------------------
// Cryptographic randomness with explicit failure
// ---------------------------------------------------------------------------

// RandomSource abstracts the entropy source so tests can inject failures.
type RandomSource interface {
	Read([]byte) (int, error)
}

var randomSource RandomSource = rand.Reader

// secureToken returns a hex token of n random bytes, failing the operation on
// any short or errored read rather than accepting zero/partial entropy.
func secureToken(n int) (string, error) {
	buf := make([]byte, n)
	read, err := randomSource.Read(buf)
	if err != nil {
		return "", fmt.Errorf("random source failed: %w", err)
	}
	if read != n {
		return "", errors.New("random source returned short read")
	}
	return hex.EncodeToString(buf), nil
}

// ---------------------------------------------------------------------------
// Transactional Owner protection
// ---------------------------------------------------------------------------

// bootstrapAdvisoryLock is a fixed advisory-lock key so concurrent bootstrap
// attempts serialize; only the first observes zero Owners.
const bootstrapAdvisoryLock int64 = 0x54_4C_4F_53_01 // "TLOS" + 1

// lockOwnerMembership takes a row lock on every Owner membership inside tx and
// returns the current Owner count, so a concurrent last-Owner mutation cannot
// interleave between the count and the delete.
func lockOwnerMembership(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM user_roles WHERE role_id = 'Owner' FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// ---------------------------------------------------------------------------
// Security event sink (Phase 3 materializes this with the outbox)
// ---------------------------------------------------------------------------

// SecurityEventIntent describes an account/security administration event to be
// recorded transactionally with the mutation that produced it.
type SecurityEventIntent struct {
	Kind       string
	ActorID    string
	SubjectID  string
	ResourceID string
}

// SecurityEventSink records a security event within the caller's transaction.
type SecurityEventSink interface {
	Record(ctx context.Context, tx pgx.Tx, intent SecurityEventIntent) error
}

// noopSecurityEventSink is the Phase 2 placeholder; Phase 3 replaces it with a
// transactional outbox writer and Phase 5 materializes notifications.
type noopSecurityEventSink struct{}

func (noopSecurityEventSink) Record(context.Context, pgx.Tx, SecurityEventIntent) error {
	return nil
}

var securityEvents SecurityEventSink = noopSecurityEventSink{}

// ---------------------------------------------------------------------------
// Login limiter
// ---------------------------------------------------------------------------

type LoginOutcome int

const (
	LoginFailure LoginOutcome = iota
	LoginSuccess
)

// LoginAttempt is a reserved in-flight login slot.
type LoginAttempt interface {
	Complete(ctx context.Context, outcome LoginOutcome) error
}

// LoginLimiter reserves a login slot before password verification.
type LoginLimiter interface {
	Reserve(ctx context.Context, username string, ip netip.Addr) (LoginAttempt, error)
}

var (
	errLoginThrottled          = errors.New("login throttled")
	errAuthThrottleUnavailable = errors.New("auth throttle unavailable")
)

// Redis-mode limits (per 15 minutes).
const (
	limitPair = 5
	limitUser = 10
	limitIP   = 20

	loginWindow     = 15 * time.Minute
	reservationTTL  = 30 * time.Second
	degradedMaxKeys = 10000
	degradedLimPair = 2
	degradedLimUser = 3
	degradedLimIP   = 5
)

// redisLoginLimiter is the primary atomic limiter; on any Redis error it falls
// back to the in-process degraded limiter.
type redisLoginLimiter struct {
	rdb      *redis.Client
	degraded *degradedLimiter
}

func newLoginLimiter(rdb *redis.Client) *redisLoginLimiter {
	return &redisLoginLimiter{rdb: rdb, degraded: newDegradedLimiter()}
}

// reserveScript atomically evaluates all three scopes and, if none is at its
// cap (failures + live reservations), records one reservation per scope.
// KEYS: failPair failUser failIP resPair resUser resIP
// ARGV: capPair capUser capIP attemptID resTTLsecs
// Returns 1 on reservation, 0 if throttled.
var reserveScript = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
local resTTL = tonumber(ARGV[5])
local caps = {tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])}
for i=1,3 do
  local fkey = KEYS[i]
  local rkey = KEYS[i+3]
  redis.call('ZREMRANGEBYSCORE', rkey, 0, now)
  local fails = tonumber(redis.call('GET', fkey) or '0')
  local live = redis.call('ZCARD', rkey)
  if fails + live >= caps[i] then
    return 0
  end
end
for i=1,3 do
  local rkey = KEYS[i+3]
  redis.call('ZADD', rkey, now + resTTL, ARGV[4])
  redis.call('EXPIRE', rkey, resTTL + 1)
end
return 1
`)

// completeScript is idempotent: the pair reservation is the authority. If the
// attempt was already completed (ZREM removes nothing there), it no-ops.
// KEYS: failPair failUser failIP resPair resUser resIP
// ARGV: attemptID outcome(1=success) windowSecs
var completeScript = redis.NewScript(`
local removedPair = redis.call('ZREM', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[5], ARGV[1])
redis.call('ZREM', KEYS[6], ARGV[1])
if removedPair == 0 then
  return 0
end
if ARGV[2] == '1' then
  redis.call('DEL', KEYS[1], KEYS[2], KEYS[3])
else
  local win = tonumber(ARGV[3])
  for i=1,3 do
    local n = redis.call('INCR', KEYS[i])
    redis.call('EXPIRE', KEYS[i], win)
  end
end
return 1
`)

func loginKeys(username string, ip netip.Addr) []string {
	u := strings.ToLower(username)
	ips := ip.String()
	return []string{
		"telos:login:fail:pair:" + u + ":" + ips,
		"telos:login:fail:user:" + u,
		"telos:login:fail:ip:" + ips,
		"telos:login:res:pair:" + u + ":" + ips,
		"telos:login:res:user:" + u,
		"telos:login:res:ip:" + ips,
	}
}

type redisAttempt struct {
	limiter   *redisLoginLimiter
	keys      []string
	attemptID string
	degraded  *degradedAttempt
}

func (l *redisLoginLimiter) Reserve(ctx context.Context, username string, ip netip.Addr) (LoginAttempt, error) {
	attemptID, err := secureToken(16)
	if err != nil {
		return nil, err
	}
	keys := loginKeys(username, ip)
	res, err := reserveScript.Run(ctx, l.rdb, keys,
		limitPair, limitUser, limitIP, attemptID, int(reservationTTL.Seconds())).Int()
	if err != nil {
		// Redis down: degrade rather than allow an unrestricted attempt.
		da, derr := l.degraded.reserve(username, ip)
		if derr != nil {
			return nil, derr
		}
		return &redisAttempt{limiter: l, degraded: da}, nil
	}
	if res == 0 {
		return nil, errLoginThrottled
	}
	return &redisAttempt{limiter: l, keys: keys, attemptID: attemptID}, nil
}

func (a *redisAttempt) Complete(ctx context.Context, outcome LoginOutcome) error {
	if a.degraded != nil {
		return a.degraded.complete(outcome)
	}
	oc := "0"
	if outcome == LoginSuccess {
		oc = "1"
	}
	return completeScript.Run(ctx, a.limiter.rdb, a.keys,
		a.attemptID, oc, int(loginWindow.Seconds())).Err()
}

// ---------------------------------------------------------------------------
// Degraded in-process limiter (Redis unavailable)
// ---------------------------------------------------------------------------

type degradedCounter struct {
	fails    int
	live     int
	expireAt time.Time
}

type degradedLimiter struct {
	mu    sync.Mutex
	items map[string]*list.Element // key -> element(*degradedEntry)
	order *list.List
}

type degradedEntry struct {
	key string
	c   degradedCounter
}

func newDegradedLimiter() *degradedLimiter {
	return &degradedLimiter{items: make(map[string]*list.Element), order: list.New()}
}

type degradedAttempt struct {
	limiter *degradedLimiter
	keys    [3]string
	done    bool
}

func (d *degradedLimiter) get(key string, now time.Time) *degradedEntry {
	if el, ok := d.items[key]; ok {
		e := el.Value.(*degradedEntry)
		if now.After(e.c.expireAt) {
			e.c = degradedCounter{}
		}
		d.order.MoveToFront(el)
		return e
	}
	// Evict LRU if at capacity.
	for len(d.items) >= degradedMaxKeys {
		back := d.order.Back()
		if back == nil {
			break
		}
		be := back.Value.(*degradedEntry)
		delete(d.items, be.key)
		d.order.Remove(back)
	}
	e := &degradedEntry{key: key, c: degradedCounter{}}
	d.items[key] = d.order.PushFront(e)
	return e
}

func (d *degradedLimiter) reserve(username string, ip netip.Addr) (*degradedAttempt, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	u := strings.ToLower(username)
	ips := ip.String()
	keys := [3]string{"pair:" + u + ":" + ips, "user:" + u, "ip:" + ips}
	caps := [3]int{degradedLimPair, degradedLimUser, degradedLimIP}
	entries := [3]*degradedEntry{}
	for i, k := range keys {
		e := d.get(k, now)
		entries[i] = e
		if e.c.fails+e.c.live >= caps[i] {
			return nil, errAuthThrottleUnavailable
		}
	}
	for _, e := range entries {
		e.c.live++
		if e.c.expireAt.IsZero() || now.After(e.c.expireAt) {
			e.c.expireAt = now.Add(loginWindow)
		}
	}
	return &degradedAttempt{limiter: d, keys: keys}, nil
}

func (a *degradedAttempt) complete(outcome LoginOutcome) error {
	a.limiter.mu.Lock()
	defer a.limiter.mu.Unlock()
	if a.done {
		return nil
	}
	a.done = true
	now := time.Now()
	for _, k := range a.keys {
		el, ok := a.limiter.items[k]
		if !ok {
			continue
		}
		e := el.Value.(*degradedEntry)
		if e.c.live > 0 {
			e.c.live--
		}
		if outcome == LoginSuccess {
			e.c.fails = 0
		} else {
			e.c.fails++
			e.c.expireAt = now.Add(loginWindow)
		}
	}
	return nil
}

var loginLimiter LoginLimiter

// ---------------------------------------------------------------------------
// Device authentication and management HTTP handlers
// ---------------------------------------------------------------------------

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

	clientAddr, ipErr := clientIP(r, securityConfig.TrustedProxyRanges)
	if ipErr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad_client_address", "The client address could not be determined.")
		return
	}

	if loginLimiter != nil {
		attempt, err := loginLimiter.Reserve(r.Context(), uc.Username, clientAddr)
		if err != nil {
			switch {
			case errors.Is(err, errLoginThrottled):
				writeAPIError(w, r, http.StatusTooManyRequests, "too_many_attempts", "Too many attempts; try again later.")
			case errors.Is(err, errAuthThrottleUnavailable):
				writeAPIError(w, r, http.StatusServiceUnavailable, "auth_throttle_unavailable", "Service temporarily unavailable.")
			default:
				writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to check rate limits")
			}
			return
		}
		defer attempt.Complete(r.Context(), LoginSuccess)
	}

	var req struct {
		DeviceName    string `json:"deviceName"`
		Platform      string `json:"platform"`
		ClientVersion string `json:"clientVersion"`
	}
	if err := decodeJSON(w, r, &req, securityConfig.AuthJSONBytes); err != nil {
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

	log.Printf("[AUDIT] Device registered: user_id=%s device_id=%s device_name=%s platform=%s client_version=%s ip=%s",
		uc.ID, deviceID, req.DeviceName, req.Platform, req.ClientVersion, clientAddr.String())

	writeJSON(w, map[string]interface{}{
		"deviceId":        deviceID,
		"refreshToken":    refreshToken,
		"accessToken":     accessToken,
		"accessExpiresIn": 900,
	})
}

func handleRefreshDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(w, r, &req, securityConfig.AuthJSONBytes); err != nil {
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

	writeJSON(w, map[string]interface{}{
		"refreshToken":    newRefresh,
		"accessToken":     newAccess,
		"accessExpiresIn": 900,
	})
}

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

	writeJSON(w, devs)
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

	writeJSON(w, map[string]interface{}{
		"ticket":    ticket,
		"expiresIn": 30,
	})
}
