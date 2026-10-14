package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A restored node comes back holding every credential that existed when the backup
// was taken. `telos-core sanitize-restore` runs once, after the database is restored and
// migrated and before any traffic is served, and in ONE transaction:
//
//   - revokes every browser session and every device access session (they share the
//     sessions table, told apart by device_id);
//   - revokes every device and consumes every outstanding refresh token;
//   - invalidates every unused, unexpired invite (marked used by no one);
//   - records what it did in restore_events.
//
// Accounts, roles and content are left exactly as they were. The command is idempotent:
// a second run changes nothing but adds its own audit row. Any failure rolls the whole
// transaction back, so a half-sanitized database never exists. It connects only through
// DATABASE_OWNER_URL (the schema owner), because the runtime role cannot write
// restore_events, and it refuses to act unless the migrations are fully applied.

var restoreSnapshotRE = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

type SanitizeResult struct {
	SessionsRevoked      int
	DevicesRevoked       int
	RefreshTokensRevoked int
	InvitesInvalidated   int
}

// sanitizeRestore applies the revocations inside tx and returns the counts it changed.
func sanitizeRestore(ctx context.Context, tx pgx.Tx, snapshot string) (SanitizeResult, error) {
	if snapshot == "" {
		snapshot = "unknown"
	}
	if snapshot != "unknown" && !restoreSnapshotRE.MatchString(snapshot) {
		return SanitizeResult{}, fmt.Errorf("sanitize-restore: the snapshot id must be 8-64 lowercase hex characters")
	}
	var result SanitizeResult
	steps := []struct {
		name  string
		sql   string
		count *int
	}{
		{"sessions", `UPDATE sessions SET revoked_at = NOW() WHERE revoked_at IS NULL`, &result.SessionsRevoked},
		{"devices", `UPDATE devices SET revoked_at = NOW() WHERE revoked_at IS NULL`, &result.DevicesRevoked},
		{"refresh tokens", `UPDATE device_refresh_tokens SET consumed_at = NOW(), expires_at = LEAST(expires_at, NOW()) WHERE consumed_at IS NULL`, &result.RefreshTokensRevoked},
		// Invalidated the way account deletion does it (used, by no one): setting expires_at
		// instead could violate expires_at > created_at when the backup's clock ran ahead.
		{"invites", `UPDATE invites SET used_at = NOW() WHERE used_at IS NULL AND expires_at > NOW()`, &result.InvitesInvalidated},
	}
	for _, step := range steps {
		tag, err := tx.Exec(ctx, step.sql)
		if err != nil {
			return SanitizeResult{}, fmt.Errorf("sanitize-restore: revoking %s: %w", step.name, err)
		}
		*step.count = int(tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO restore_events (source_snapshot, sessions_revoked, devices_revoked, refresh_tokens_revoked, invites_invalidated)
		VALUES ($1, $2, $3, $4, $5)`,
		snapshot, result.SessionsRevoked, result.DevicesRevoked, result.RefreshTokensRevoked, result.InvitesInvalidated); err != nil {
		return SanitizeResult{}, fmt.Errorf("sanitize-restore: recording the restore event: %w", err)
	}
	return result, nil
}

// runSanitizeRestore verifies the migrations, then sanitizes in a transaction.
func runSanitizeRestore(ctx context.Context, pool *pgxpool.Pool, snapshot string) (SanitizeResult, error) {
	if _, err := VerifyMigrations(ctx, pool, migrationsFS); err != nil {
		return SanitizeResult{}, fmt.Errorf("sanitize-restore: refusing to run: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return SanitizeResult{}, fmt.Errorf("sanitize-restore: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a rollback after commit is a no-op
	result, err := sanitizeRestore(ctx, tx, snapshot)
	if err != nil {
		return SanitizeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SanitizeResult{}, fmt.Errorf("sanitize-restore: commit: %w", err)
	}
	return result, nil
}

// runSanitizeRestoreCommand is the `sanitize-restore` process entry. It is dispatched
// before any serve-time initialization and exits the process itself.
func runSanitizeRestoreCommand(args []string) int {
	snapshot := "unknown"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--snapshot" && i+1 < len(args):
			snapshot = strings.TrimSpace(args[i+1])
			i++
		default:
			fmt.Fprintln(os.Stderr, "usage: telos-core sanitize-restore [--snapshot SNAPSHOT_ID]")
			return 2
		}
	}
	url := os.Getenv("DATABASE_OWNER_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "sanitize-restore: DATABASE_OWNER_URL is not set; the schema owner connection is required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sanitize-restore: cannot connect to the database")
		return 1
	}
	defer pool.Close()
	result, err := runSanitizeRestore(ctx, pool, snapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log.Printf("sanitize-restore: revoked %d sessions, %d devices, %d refresh tokens; invalidated %d unused invites",
		result.SessionsRevoked, result.DevicesRevoked, result.RefreshTokensRevoked, result.InvitesInvalidated)
	return 0
}
