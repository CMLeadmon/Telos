package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseConfig holds every pool and per-connection timeout. Zero/negative or
// out-of-range values are rejected at load.
type DatabaseConfig struct {
	URL                    string
	MaxConns               int32
	MinConns               int32
	MaxConnLifetime        time.Duration
	MaxConnIdleTime        time.Duration
	ConnectTimeout         time.Duration
	StatementTimeout       time.Duration
	LockTimeout            time.Duration
	IdleTransactionTimeout time.Duration
	RequestTimeout         time.Duration
}

func defaultDatabaseConfig(url string) DatabaseConfig {
	return DatabaseConfig{
		URL:                    url,
		MaxConns:               20,
		MinConns:               2,
		MaxConnLifetime:        time.Hour,
		MaxConnIdleTime:        30 * time.Minute,
		ConnectTimeout:         5 * time.Second,
		StatementTimeout:       5 * time.Second,
		LockTimeout:            2 * time.Second,
		IdleTransactionTimeout: 5 * time.Second,
		RequestTimeout:         10 * time.Second,
	}
}

func (c DatabaseConfig) validate() error {
	if c.URL == "" {
		return errors.New("database URL is required")
	}
	if c.MaxConns < 1 || c.MinConns < 0 || c.MinConns > c.MaxConns {
		return fmt.Errorf("invalid connection bounds min=%d max=%d", c.MinConns, c.MaxConns)
	}
	for name, d := range map[string]time.Duration{
		"connect":     c.ConnectTimeout,
		"statement":   c.StatementTimeout,
		"lock":        c.LockTimeout,
		"idleTxn":     c.IdleTransactionTimeout,
		"request":     c.RequestTimeout,
		"maxLifetime": c.MaxConnLifetime,
		"maxIdle":     c.MaxConnIdleTime,
	} {
		if d <= 0 {
			return fmt.Errorf("%s timeout must be positive", name)
		}
	}
	return nil
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := getenv(key); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return fallback
}

// getenv is a tiny indirection so tests can override without os coupling here.
var getenv = func(k string) string { return "" }

// NewDatabasePool builds a pgx pool that applies the statement/lock/idle-in-
// transaction timeouts on every connection.
func NewDatabasePool(ctx context.Context, cfg DatabaseConfig) (*pgxpool.Pool, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	pcfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, err
	}
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	stmtMS := cfg.StatementTimeout.Milliseconds()
	lockMS := cfg.LockTimeout.Milliseconds()
	idleMS := cfg.IdleTransactionTimeout.Milliseconds()
	pcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf(
			"SET statement_timeout = %d; SET lock_timeout = %d; SET idle_in_transaction_session_timeout = %d",
			stmtMS, lockMS, idleMS))
		return err
	}
	return pgxpool.NewWithConfig(ctx, pcfg)
}

// ---------------------------------------------------------------------------
// Aggregate authenticated-user load (replaces per-request query fan-out)
// ---------------------------------------------------------------------------

// LoadAuthenticatedUser resolves the user, roles, and sorted effective
// permissions for a session token hash in a single query.
func LoadAuthenticatedUser(ctx context.Context, q DBTX, tokenHash string) (*UserContext, error) {
	var (
		userID      string
		username    string
		active      bool
		expiresAt   time.Time
		displayName *string
		avatarID    *string
		roles       []string
		perms       []string
	)
	err := q.QueryRow(ctx, `
		SELECT u.id, u.username, u.active, s.expires_at, u.display_name,
		       u.avatar_file_id::text,
		       COALESCE((SELECT array_agg(ur.role_id ORDER BY ur.role_id)
		                 FROM user_roles ur WHERE ur.user_id = u.id), '{}'),
		       COALESCE((SELECT array_agg(DISTINCT rp.permission_id ORDER BY rp.permission_id)
		                 FROM role_permissions rp
		                 JOIN user_roles ur2 ON ur2.role_id = rp.role_id
		                 WHERE ur2.user_id = u.id), '{}')
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL
	`, tokenHash).Scan(&userID, &username, &active, &expiresAt, &displayName, &avatarID, &roles, &perms)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, errors.New("account disabled")
	}
	if time.Now().After(expiresAt) {
		return nil, errors.New("session expired")
	}
	uc := &UserContext{ID: userID, Username: username, Roles: roles, Permissions: perms}
	if displayName != nil {
		uc.DisplayName = *displayName
	}
	uc.HasAvatar = avatarID != nil
	return uc, nil
}

// ---------------------------------------------------------------------------
// Bounded session-touch worker (replaces unbounded per-request goroutines)
// ---------------------------------------------------------------------------

type sessionTouchWorker struct {
	ch      chan string
	pool    *pgxpool.Pool
	wg      sync.WaitGroup
	dropped atomic64
}

type atomic64 struct {
	mu sync.Mutex
	n  int64
}

func (a *atomic64) inc() { a.mu.Lock(); a.n++; a.mu.Unlock() }
func (a *atomic64) load() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

func newSessionTouchWorker(pool *pgxpool.Pool) *sessionTouchWorker {
	return &sessionTouchWorker{ch: make(chan string, 256), pool: pool}
}

// Touch enqueues a session hash for a coalesced last-seen update. When the
// buffer is full it drops the redundant touch (recording a metric) rather than
// blocking the request or spawning a goroutine.
func (w *sessionTouchWorker) Touch(tokenHash string) {
	select {
	case w.ch <- tokenHash:
	default:
		w.dropped.inc()
	}
}

// Run drains touches in batches of up to 100, deduplicating within a batch,
// until the context is cancelled and the channel is drained.
func (w *sessionTouchWorker) Run(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		batch := make(map[string]struct{}, 100)
		flush := func() {
			if len(batch) == 0 || w.pool == nil {
				return
			}
			hashes := make([]string, 0, len(batch))
			for h := range batch {
				hashes = append(hashes, h)
			}
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			w.pool.Exec(fctx, `
				UPDATE sessions SET last_seen_at = NOW()
				WHERE token_hash = ANY($1)
				  AND (last_seen_at IS NULL OR last_seen_at < NOW() - INTERVAL '60 seconds')
			`, hashes)
			cancel()
			batch = make(map[string]struct{}, 100)
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case h := <-w.ch:
				batch[h] = struct{}{}
				if len(batch) >= 100 {
					flush()
				}
			case <-ticker.C:
				flush()
			case <-ctx.Done():
				// Drain remaining without blocking.
				for {
					select {
					case h := <-w.ch:
						batch[h] = struct{}{}
					default:
						flush()
						return
					}
				}
			}
		}
	}()
}

func (w *sessionTouchWorker) Wait() { w.wg.Wait() }

var sessionTouches *sessionTouchWorker

// splitCSV is a small helper for list configuration values.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
