package main

import (
	"testing"
	"time"
)

func TestDatabaseConfigValidation(t *testing.T) {
	base := defaultDatabaseConfig("postgres://x/y")
	if err := base.validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}

	bad := []DatabaseConfig{
		func() DatabaseConfig { c := base; c.URL = ""; return c }(),
		func() DatabaseConfig { c := base; c.MaxConns = 0; return c }(),
		func() DatabaseConfig { c := base; c.MinConns = -1; return c }(),
		func() DatabaseConfig { c := base; c.MinConns = 30; return c }(), // > max
		func() DatabaseConfig { c := base; c.StatementTimeout = 0; return c }(),
		func() DatabaseConfig { c := base; c.LockTimeout = -time.Second; return c }(),
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestSessionTouchWorkerSaturationDrops(t *testing.T) {
	// A worker whose channel fills drops redundant touches instead of blocking.
	w := &sessionTouchWorker{ch: make(chan string, 4), pool: nil}
	for i := 0; i < 100; i++ {
		w.Touch("hash")
	}
	if w.dropped.load() == 0 {
		t.Fatal("expected drops once the buffer saturated")
	}
	// Never blocks: reaching here means Touch returned promptly.
}
