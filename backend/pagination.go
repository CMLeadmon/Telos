package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// CursorValue is one typed component of a seek tuple.
type CursorValue struct {
	Kind  string `json:"kind"` // "time", "text", "int", or "uuid"
	Value string `json:"value"`
}

// PageCursor is the canonical, signed pagination envelope.
type PageCursor struct {
	Version    uint8         `json:"version"`
	KeyID      string        `json:"keyId"`
	Scope      string        `json:"scope"`
	Sort       string        `json:"sort"`
	FilterHash string        `json:"filterHash"`
	Values     []CursorValue `json:"values"`
	ID         string        `json:"id"`
	IssuedAt   time.Time     `json:"issuedAt"`
}

const (
	cursorVersion  = 1
	cursorLifetime = 24 * time.Hour
)

var errInvalidCursor = errors.New("invalid_cursor")

// CursorCodec signs and verifies cursor envelopes.
type CursorCodec interface {
	Encode(scope string, cursor PageCursor) (string, error)
	Decode(scope string, token string) (PageCursor, error)
}

// keyring holds the active signing key plus any retiring keys still accepted
// for decode during rotation.
type cursorKeyring struct {
	activeID string
	keys     map[string][]byte // keyID -> >=32 random bytes
}

func (k *cursorKeyring) validate() error {
	if k.activeID == "" || len(k.keys) == 0 {
		return errors.New("cursor keyring requires an active key")
	}
	if _, ok := k.keys[k.activeID]; !ok {
		return errors.New("active cursor key is not present in the keyring")
	}
	seen := map[string]struct{}{}
	for id, b := range k.keys {
		if len(b) < 32 {
			return fmt.Errorf("cursor key %q is shorter than 32 bytes", id)
		}
		fp := string(b)
		if _, dup := seen[fp]; dup {
			return errors.New("cursor keys must be bytewise distinct")
		}
		seen[fp] = struct{}{}
	}
	return nil
}

type hmacCursorCodec struct{ ring *cursorKeyring }

// canonicalCursorBytes serializes the envelope deterministically (Go's
// encoding/json emits struct fields in declaration order, which is stable).
func canonicalCursorBytes(c PageCursor) ([]byte, error) { return json.Marshal(c) }

func (h *hmacCursorCodec) Encode(scope string, cursor PageCursor) (string, error) {
	cursor.Version = cursorVersion
	cursor.Scope = scope
	cursor.KeyID = h.ring.activeID
	if cursor.IssuedAt.IsZero() {
		return "", errors.New("cursor issuedAt must be set")
	}
	body, err := canonicalCursorBytes(cursor)
	if err != nil {
		return "", err
	}
	key := h.ring.keys[h.ring.activeID]
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

func (h *hmacCursorCodec) Decode(scope string, token string) (PageCursor, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return PageCursor{}, errInvalidCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return PageCursor{}, errInvalidCursor
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return PageCursor{}, errInvalidCursor
	}
	var c PageCursor
	if err := json.Unmarshal(body, &c); err != nil {
		return PageCursor{}, errInvalidCursor
	}
	key, ok := h.ring.keys[c.KeyID]
	if !ok {
		return PageCursor{}, errInvalidCursor
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return PageCursor{}, errInvalidCursor
	}
	if c.Version != cursorVersion || c.Scope != scope {
		return PageCursor{}, errInvalidCursor
	}
	if time.Since(c.IssuedAt) > cursorLifetime || c.IssuedAt.After(time.Now().Add(time.Minute)) {
		return PageCursor{}, errInvalidCursor
	}
	return c, nil
}

var cursorCodec CursorCodec

// loadCursorKeyring reads the mode-0600 TELOS_CURSOR_KEYS_FILE JSON
// ({"active":"id","keys":{"id":"base64",...}}). In development, a keyring may
// be synthesized so cursors work without an operator file.
func loadCursorKeyring(path, env string) (*cursorKeyring, error) {
	if path == "" {
		if env == "development" {
			b := sha256.Sum256([]byte("telos-dev-cursor-key"))
			return &cursorKeyring{activeID: "dev", keys: map[string][]byte{"dev": b[:]}}, nil
		}
		return nil, errors.New("TELOS_CURSOR_KEYS_FILE is not set")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("TELOS_CURSOR_KEYS_FILE must be mode 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	ring := &cursorKeyring{activeID: raw.Active, keys: map[string][]byte{}}
	for id, b64 := range raw.Keys {
		kb, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("cursor key %q is not valid base64", id)
		}
		ring.keys[id] = kb
	}
	if err := ring.validate(); err != nil {
		return nil, err
	}
	return ring, nil
}

// ---------------------------------------------------------------------------
// List-policy registry
// ---------------------------------------------------------------------------

// ListMode distinguishes cursor-paginated lists from deliberately finite ones.
type ListMode string

const (
	ListCursor ListMode = "cursor"
	ListCapped ListMode = "capped"
)

// ListPolicy is the registered contract for one list-returning route.
type ListPolicy struct {
	Scope       string
	Mode        ListMode
	DefaultSort string
	AllowedSort map[string]struct{}
	HardCap     int // for ListCapped, and the max page size for ListCursor
}

var listPolicies = map[string]ListPolicy{}

func registerList(p ListPolicy) {
	listPolicies[p.Scope] = p
}

// ListPolicyFor returns the registered policy for a scope.
func ListPolicyFor(scope string) (ListPolicy, bool) {
	p, ok := listPolicies[scope]
	return p, ok
}

func init() {
	// Cursor-paginated lists (default 50, max 100).
	for _, s := range []struct {
		scope, sort string
	}{
		{"chat.history", "created_desc"},
		{"admin.users", "created_desc"},
		{"admin.invites", "created_desc"},
		{"admin.sessions", "created_desc"},
		{"channel.members", "joined_desc"},
		{"files.audit", "created_desc"},
		{"notifications", "created_desc"},
		{"channel.roots", "created_desc"},
		{"channel.replies", "created_asc"},
		{"media.list", "position_asc"},
	} {
		registerList(ListPolicy{
			Scope: s.scope, Mode: ListCursor, DefaultSort: s.sort,
			AllowedSort: map[string]struct{}{s.sort: {}}, HardCap: 100,
		})
	}
	// Deliberately finite lists with tested hard caps.
	for scope, cap := range map[string]int{
		"channels":          100,
		"roles":             100,
		"permissions":       64,
		"message.reactions": 100,
		"channel.pins":      100,
		"search.users":      15,
		"search.channels":   15,
		"search.files":      15,
		"search.media":      15,
		"search.library":    15,
	} {
		registerList(ListPolicy{Scope: scope, Mode: ListCapped, HardCap: cap})
	}
}

// PageRequest is a parsed list request.
type PageRequest struct {
	Scope string
	Sort  string
	Limit int
	After string
}

// Page is a page of items plus the opaque next cursor.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// resolvePageRequest validates a request against its registered policy,
// clamping the limit to [1,100] with a default of 50.
func resolvePageRequest(scope, sort, after string, limit int) (PageRequest, ListPolicy, error) {
	p, ok := ListPolicyFor(scope)
	if !ok || p.Mode != ListCursor {
		return PageRequest{}, ListPolicy{}, errors.New("scope is not a registered cursor list")
	}
	if sort == "" {
		sort = p.DefaultSort
	}
	if _, ok := p.AllowedSort[sort]; !ok {
		return PageRequest{}, ListPolicy{}, errInvalidCursor
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return PageRequest{Scope: scope, Sort: sort, Limit: limit, After: after}, p, nil
}

// filterHash produces a stable hash of a route's normalized filter set so a
// cursor cannot be replayed across different filters.
func filterHash(parts ...string) string {
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}
