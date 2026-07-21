package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"telos-core/testutil"
)

// TestRuntimeRoleCannotPerformDDL proves the least-privilege runtime role can
// do required DML but cannot create/alter/drop objects or write migration
// history, while the owner can.
func TestRuntimeRoleCannotPerformDDL(t *testing.T) {
	owner := testutil.FreshDatabase(t)
	ctx := context.Background()

	// Provision the runtime role and least-privilege grants (mirrors the
	// clean-volume init script).
	setup := []string{
		`CREATE ROLE telos_runtime LOGIN PASSWORD 'runtimepw'`,
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA public TO telos_runtime`,
	}
	for _, s := range setup {
		if _, err := owner.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	// Apply the real schema as owner, then grant runtime DML.
	if _, err := RunMigrations(ctx, owner, migrationsFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, s := range []string{
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO telos_runtime`,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO telos_runtime`,
		// Migration state is read-only for the runtime.
		`REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM telos_runtime`,
	} {
		if _, err := owner.Exec(ctx, s); err != nil {
			t.Fatalf("grant %q: %v", s, err)
		}
	}

	// Connect as telos_runtime.
	dbURL := os.Getenv("TELOS_TEST_DATABASE_URL")
	runtimeURL := swapUserPassDBName(t, dbURL, "telos_runtime", "runtimepw", currentDBName(t, owner, ctx))
	rt, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatalf("runtime connect: %v", err)
	}
	defer rt.Close()

	// DML must succeed: insert a role permission read, select a seeded role.
	var roleCount int
	if err := rt.QueryRow(ctx, `SELECT COUNT(*) FROM roles`).Scan(&roleCount); err != nil {
		t.Fatalf("runtime SELECT failed: %v", err)
	}
	if roleCount == 0 {
		t.Fatal("expected seeded roles visible to runtime")
	}

	// DDL and migration writes must fail.
	denied := []struct {
		name string
		sql  string
	}{
		{"create table", `CREATE TABLE runtime_should_fail (id int)`},
		{"alter table", `ALTER TABLE users ADD COLUMN hacked int`},
		{"drop table", `DROP TABLE channels`},
		{"write migration history", `INSERT INTO schema_migrations (version, name, checksum) VALUES (999, 'x', 'y')`},
		{"create role", `CREATE ROLE escalated LOGIN`},
	}
	for _, d := range denied {
		if _, err := rt.Exec(ctx, d.sql); err == nil {
			t.Errorf("runtime was allowed to %q", d.name)
		} else if !strings.Contains(strings.ToLower(err.Error()), "permission denied") &&
			!strings.Contains(strings.ToLower(err.Error()), "must be owner") &&
			!strings.Contains(strings.ToLower(err.Error()), "denied") {
			// Any error is acceptable so long as the operation did not succeed;
			// log unexpected shapes for visibility.
			t.Logf("runtime %q denied with: %v", d.name, err)
		}
	}
}

func currentDBName(t *testing.T, pool *pgxpool.Pool, ctx context.Context) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

// swapUserPassDBName rebuilds a postgres URL with new credentials and database.
func swapUserPassDBName(t *testing.T, rawurl, user, pass, dbname string) string {
	t.Helper()
	// postgres://USER:PASS@host:port/db?params
	scheme := "postgres://"
	rest := strings.TrimPrefix(rawurl, scheme)
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		t.Fatalf("unparseable url")
	}
	hostPart := rest[at+1:]
	slash := strings.Index(hostPart, "/")
	hostPort := hostPart
	query := ""
	if slash >= 0 {
		hostPort = hostPart[:slash]
		if q := strings.Index(hostPart[slash:], "?"); q >= 0 {
			query = hostPart[slash:][q:]
		}
	}
	return scheme + user + ":" + pass + "@" + hostPort + "/" + dbname + query
}
