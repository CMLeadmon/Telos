// Package testutil provides a disposable PostgreSQL + Redis fixture for
// backend integration tests. Tests call Setup(t), which reads
// TELOS_TEST_DATABASE_URL and TELOS_TEST_REDIS_URL (provided by
// scripts/test-backend.sh) and calls t.Skip when they are unset, so an
// ordinary `go test ./...` still runs the pure-unit suite.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Fixture holds live connections to the disposable dependencies.
type Fixture struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
}

// Setup connects to the disposable PostgreSQL and Redis, applies every
// migration in numeric order, and resets both stores to a clean state. It
// registers cleanup and skips the test when the fixture env is not present.
func Setup(t *testing.T) *Fixture {
	t.Helper()
	dbURL := os.Getenv("TELOS_TEST_DATABASE_URL")
	redisURL := os.Getenv("TELOS_TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("integration fixture unavailable: set TELOS_TEST_DATABASE_URL and TELOS_TEST_REDIS_URL (see scripts/test-backend.sh)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}

	ropt, err := redis.ParseURL(redisURL)
	if err != nil {
		pool.Close()
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(ropt)

	f := &Fixture{DB: pool, Redis: rdb}
	if err := f.applyMigrations(ctx); err != nil {
		f.close()
		t.Fatalf("apply migrations: %v", err)
	}
	if err := f.Reset(ctx); err != nil {
		f.close()
		t.Fatalf("reset fixture: %v", err)
	}
	t.Cleanup(f.close)
	return f
}

func migrationsDir() string {
	_, self, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(self), "..", "db", "migrations")
}

func (f *Fixture) applyMigrations(ctx context.Context) error {
	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if _, err := f.DB.Exec(ctx, string(sqlBytes)); err != nil {
			return err
		}
	}
	return nil
}

// Reset truncates every application table and flushes Redis so each test
// starts from a clean, seeded-roles state.
func (f *Fixture) Reset(ctx context.Context) error {
	// Discover application tables (skip nothing; schema_migrations is not used
	// by the embedded-schema approach). Restart identity, cascade FKs.
	rows, err := f.DB.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename NOT IN ('roles','permissions','role_permissions')
	`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, `"`+name+`"`)
	}
	rows.Close()
	if len(tables) > 0 {
		stmt := "TRUNCATE " + join(tables, ", ") + " RESTART IDENTITY CASCADE"
		if _, err := f.DB.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return f.Redis.FlushDB(ctx).Err()
}

func (f *Fixture) close() {
	f.DB.Close()
	_ = f.Redis.Close()
}

func join(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
