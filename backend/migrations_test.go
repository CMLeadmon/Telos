package main

import (
	"errors"
	"testing"
	"testing/fstest"
)

func fsFrom(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m["db/migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestDiscoverMigrationsValidation(t *testing.T) {
	t.Run("contiguous ok", func(t *testing.T) {
		ms, err := DiscoverMigrations(fsFrom(map[string]string{
			"0001_a.sql": "SELECT 1;",
			"0002_b.sql": "SELECT 2;",
		}), "db/migrations")
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 2 || ms[0].Version != 1 || ms[1].Name != "b" {
			t.Fatalf("unexpected: %+v", ms)
		}
		if ms[0].SHA256 == "" {
			t.Fatal("checksum not computed")
		}
	})

	t.Run("malformed name", func(t *testing.T) {
		_, err := DiscoverMigrations(fsFrom(map[string]string{"1_a.sql": "x"}), "db/migrations")
		if !errors.Is(err, errMigrationMalformed) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("gap rejected", func(t *testing.T) {
		_, err := DiscoverMigrations(fsFrom(map[string]string{
			"0001_a.sql": "x", "0003_c.sql": "y",
		}), "db/migrations")
		if !errors.Is(err, errMigrationGap) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("must start at 0001", func(t *testing.T) {
		_, err := DiscoverMigrations(fsFrom(map[string]string{"0002_a.sql": "x"}), "db/migrations")
		if !errors.Is(err, errMigrationGap) {
			t.Fatalf("err = %v", err)
		}
	})
}

func mig(v int, name, sum string) Migration { return Migration{Version: v, Name: name, SHA256: sum} }
func app(v int, name, sum string) AppliedMigration {
	return AppliedMigration{Version: v, Name: name, Checksum: sum}
}

func TestPlanMigrations(t *testing.T) {
	local := []Migration{mig(1, "a", "h1"), mig(2, "b", "h2"), mig(3, "c", "h3")}

	t.Run("empty applies all", func(t *testing.T) {
		p, err := PlanMigrations(local, nil)
		if err != nil || len(p) != 3 {
			t.Fatalf("p=%v err=%v", p, err)
		}
	})

	t.Run("partial applies remainder", func(t *testing.T) {
		p, err := PlanMigrations(local, []AppliedMigration{app(1, "a", "h1")})
		if err != nil || len(p) != 2 || p[0].Version != 2 {
			t.Fatalf("p=%v err=%v", p, err)
		}
	})

	t.Run("changed checksum rejected", func(t *testing.T) {
		_, err := PlanMigrations(local, []AppliedMigration{app(1, "a", "DIFFERENT")})
		if !errors.Is(err, errMigrationChanged) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("changed name rejected", func(t *testing.T) {
		_, err := PlanMigrations(local, []AppliedMigration{app(1, "renamed", "h1")})
		if !errors.Is(err, errMigrationChanged) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("future version rejected", func(t *testing.T) {
		_, err := PlanMigrations(local, []AppliedMigration{
			app(1, "a", "h1"), app(2, "b", "h2"), app(3, "c", "h3"), app(4, "d", "h4"),
		})
		if !errors.Is(err, errMigrationFuture) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("applied gap rejected", func(t *testing.T) {
		_, err := PlanMigrations(local, []AppliedMigration{app(1, "a", "h1"), app(3, "c", "h3")})
		if !errors.Is(err, errMigrationGap) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("legacy version-only tolerated for planning", func(t *testing.T) {
		// Empty name/checksum (legacy rows) do not trip the change check.
		p, err := PlanMigrations(local, []AppliedMigration{app(1, "", ""), app(2, "", "")})
		if err != nil || len(p) != 1 || p[0].Version != 3 {
			t.Fatalf("p=%v err=%v", p, err)
		}
	})
}

func TestChecksumSetDeterministic(t *testing.T) {
	a := checksumSet([]Migration{mig(1, "a", "h1"), mig(2, "b", "h2")})
	b := checksumSet([]Migration{mig(1, "a", "h1"), mig(2, "b", "h2")})
	if a != b || a == "" {
		t.Fatalf("checksum set not deterministic: %q %q", a, b)
	}
	c := checksumSet([]Migration{mig(1, "a", "h1"), mig(2, "b", "CHANGED")})
	if c == a {
		t.Fatal("checksum set did not change with content")
	}
}
