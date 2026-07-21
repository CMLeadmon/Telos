package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the shared read/write surface implemented by *pgxpool.Pool,
// *pgxpool.Conn, and pgx.Tx.
type DBTX interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// migrationAdvisoryLock serializes migration runs across processes.
const migrationAdvisoryLock int64 = 0x54_4C_4F_53_4D // "TLOSM"

// migrationLockTimeout bounds how long a migrator waits for the advisory lock.
const migrationLockTimeout = 30 * time.Second

var migrationFileRE = regexp.MustCompile(`^([0-9]{4})_([a-z0-9_]+)\.sql$`)

// Migration is a discovered local migration file.
type Migration struct {
	Version int
	Name    string
	SQL     []byte
	SHA256  string
}

// AppliedMigration is a row recorded in schema_migrations.
type AppliedMigration struct {
	Version  int
	Name     string
	Checksum string
}

// MigrationState summarizes the migration position after a run/verify.
type MigrationState struct {
	CurrentVersion  int
	ExpectedVersion int
	ChecksumSet     string
}

var (
	errMigrationGap         = errors.New("migration versions are not contiguous")
	errMigrationChanged     = errors.New("applied migration history changed")
	errMigrationFuture      = errors.New("database has an unknown future migration")
	errMigrationMalformed   = errors.New("malformed migration filename")
	errMigrationDuplicate   = errors.New("duplicate migration version")
	errMigrationLegacyDirty = errors.New("legacy migration history is not contiguous; refusing to bless")
)

// DiscoverMigrations reads, validates, and checksums every migration under
// dir. Versions must be contiguous four-digit numbers starting at 0001 with
// no duplicates and canonical filenames.
func DiscoverMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("%w: %s", errMigrationMalformed, e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		if seen[version] {
			return nil, fmt.Errorf("%w: %04d", errMigrationDuplicate, version)
		}
		seen[version] = true
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		out = append(out, Migration{
			Version: version,
			Name:    m[2],
			SQL:     data,
			SHA256:  hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("%w: expected %04d, found %04d", errMigrationGap, i+1, m.Version)
		}
	}
	return out, nil
}

// PlanMigrations validates that applied history is an immutable prefix of the
// local set and returns the pending migrations to apply.
func PlanMigrations(local []Migration, applied []AppliedMigration) ([]Migration, error) {
	byVersion := map[int]AppliedMigration{}
	maxApplied := 0
	for _, a := range applied {
		byVersion[a.Version] = a
		if a.Version > maxApplied {
			maxApplied = a.Version
		}
	}
	if maxApplied > len(local) {
		return nil, fmt.Errorf("%w: applied %04d has no local migration", errMigrationFuture, maxApplied)
	}
	// Applied versions must be exactly 1..maxApplied (no gaps) and match local
	// name + checksum.
	for v := 1; v <= maxApplied; v++ {
		a, ok := byVersion[v]
		if !ok {
			return nil, fmt.Errorf("%w: applied history is missing %04d", errMigrationGap, v)
		}
		lm := local[v-1]
		if a.Checksum != "" && a.Checksum != lm.SHA256 {
			return nil, fmt.Errorf("%w: %04d checksum differs", errMigrationChanged, v)
		}
		if a.Name != "" && a.Name != lm.Name {
			return nil, fmt.Errorf("%w: %04d name differs", errMigrationChanged, v)
		}
	}
	var pending []Migration
	for _, m := range local {
		if _, ok := byVersion[m.Version]; !ok {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

func checksumSet(local []Migration) string {
	h := sha256.New()
	for _, m := range local {
		fmt.Fprintf(h, "%04d:%s\n", m.Version, m.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// loadApplied reads schema_migrations, tolerating the legacy version-only
// shape by returning empty name/checksum for rows that predate the columns.
func loadApplied(ctx context.Context, q DBTX) ([]AppliedMigration, error) {
	rows, err := q.Query(ctx, `
		SELECT version, COALESCE(name, ''), COALESCE(checksum, '')
		FROM schema_migrations ORDER BY version
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ensureMigrationTable creates or upgrades schema_migrations to carry name and
// checksum columns.
func ensureMigrationTable(ctx context.Context, q DBTX) error {
	_, err := q.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			name TEXT,
			checksum TEXT,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS name TEXT;
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT;
		-- Migration history is read-only for the runtime role: strip any write
		-- privilege that default privileges may have granted at table creation.
		DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_runtime') THEN
				REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM telos_runtime;
			END IF;
		END $$;
	`)
	return err
}

// blessLegacy backfills name/checksum for pre-existing version-only rows, but
// only when the applied versions are exactly contiguous from 0001 and each has
// a corresponding local migration. Otherwise it refuses rather than blessing an
// unknown history.
func blessLegacy(ctx context.Context, tx pgx.Tx, local []Migration, applied []AppliedMigration) error {
	needs := false
	for _, a := range applied {
		if a.Checksum == "" {
			needs = true
			break
		}
	}
	if !needs {
		return nil
	}
	maxApplied := 0
	present := map[int]bool{}
	for _, a := range applied {
		present[a.Version] = true
		if a.Version > maxApplied {
			maxApplied = a.Version
		}
	}
	if maxApplied > len(local) {
		return fmt.Errorf("%w: applied %04d beyond local set", errMigrationFuture, maxApplied)
	}
	for v := 1; v <= maxApplied; v++ {
		if !present[v] {
			return errMigrationLegacyDirty
		}
	}
	for v := 1; v <= maxApplied; v++ {
		lm := local[v-1]
		if _, err := tx.Exec(ctx, `
			UPDATE schema_migrations SET name = $2, checksum = $3
			WHERE version = $1 AND (checksum IS NULL OR checksum = '')
		`, v, lm.Name, lm.SHA256); err != nil {
			return err
		}
	}
	return nil
}

// RunMigrations applies pending migrations under a single advisory lock, each
// in its own transaction, recording version/name/checksum.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (MigrationState, error) {
	local, err := DiscoverMigrations(fsys, "db/migrations")
	if err != nil {
		return MigrationState{}, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return MigrationState{}, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", migrationLockTimeout.Milliseconds())); err != nil {
		return MigrationState{}, err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		return MigrationState{}, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationAdvisoryLock)

	if err := ensureMigrationTable(ctx, conn); err != nil {
		return MigrationState{}, err
	}
	applied, err := loadApplied(ctx, conn)
	if err != nil {
		return MigrationState{}, err
	}

	// Bless a legacy version-only history (contiguous) before planning.
	if len(applied) > 0 {
		btx, err := conn.Begin(ctx)
		if err != nil {
			return MigrationState{}, err
		}
		if err := blessLegacy(ctx, btx, local, applied); err != nil {
			btx.Rollback(ctx)
			return MigrationState{}, err
		}
		if err := btx.Commit(ctx); err != nil {
			return MigrationState{}, err
		}
		if applied, err = loadApplied(ctx, conn); err != nil {
			return MigrationState{}, err
		}
	}

	pending, err := PlanMigrations(local, applied)
	if err != nil {
		return MigrationState{}, err
	}

	for _, m := range pending {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return MigrationState{}, err
		}
		if _, err := tx.Exec(ctx, string(m.SQL)); err != nil {
			tx.Rollback(ctx)
			return MigrationState{}, fmt.Errorf("migration %04d failed: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)
		`, m.Version, m.Name, m.SHA256); err != nil {
			tx.Rollback(ctx)
			return MigrationState{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MigrationState{}, err
		}
	}

	return MigrationState{
		CurrentVersion:  len(local),
		ExpectedVersion: len(local),
		ChecksumSet:     checksumSet(local),
	}, nil
}

// runMigrateCommand is the one-shot `telos-core migrate` entrypoint. It
// connects with the schema-owner URL (DATABASE_OWNER_URL, falling back to
// DATABASE_URL for single-role development), applies pending migrations under
// the advisory lock, and exits.
func runMigrateCommand() {
	url := os.Getenv("DATABASE_OWNER_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		log.Fatal("migrate: DATABASE_OWNER_URL (or DATABASE_URL) is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		log.Fatalf("migrate: connect failed: %v", err)
	}
	defer pool.Close()
	state, err := RunMigrations(ctx, pool, migrationsFS)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("migrate: applied through version %d (checksum set %s)", state.CurrentVersion, state.ChecksumSet[:12])
}

// VerifyMigrations confirms the applied history exactly matches the local set
// with nothing pending. Used by the serve path to fail readiness on a
// mismatch without applying anything.
func VerifyMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (MigrationState, error) {
	local, err := DiscoverMigrations(fsys, "db/migrations")
	if err != nil {
		return MigrationState{}, err
	}
	applied, err := loadApplied(ctx, pool)
	if err != nil {
		return MigrationState{}, err
	}
	pending, err := PlanMigrations(local, applied)
	if err != nil {
		return MigrationState{}, err
	}
	current := 0
	for _, a := range applied {
		if a.Version > current {
			current = a.Version
		}
	}
	state := MigrationState{
		CurrentVersion:  current,
		ExpectedVersion: len(local),
		ChecksumSet:     checksumSet(local),
	}
	if len(pending) > 0 {
		return state, fmt.Errorf("%d migration(s) pending; run the migrator", len(pending))
	}
	return state, nil
}
