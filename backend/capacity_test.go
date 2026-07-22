package main

import "testing"

func TestStreamCapacityGlobalBound(t *testing.T) {
	c := newStreamCapacity(2, 10)
	r1, ok1 := c.Acquire("a")
	r2, ok2 := c.Acquire("b")
	if !ok1 || !ok2 {
		t.Fatal("first two admissions should succeed")
	}
	if _, ok := c.Acquire("c"); ok {
		t.Fatal("global bound exceeded")
	}
	// Releasing frees a slot; releasing twice does not over-credit.
	r1()
	r1()
	if c.InFlight() != 1 {
		t.Fatalf("InFlight = %d, want 1", c.InFlight())
	}
	if _, ok := c.Acquire("c"); !ok {
		t.Fatal("slot should be available after release")
	}
	r2()
}

func TestStreamCapacityPerUserBound(t *testing.T) {
	c := newStreamCapacity(100, 2)
	c.Acquire("u")
	c.Acquire("u")
	if _, ok := c.Acquire("u"); ok {
		t.Fatal("per-user bound exceeded")
	}
	// A different user is unaffected.
	if _, ok := c.Acquire("other"); !ok {
		t.Fatal("second user wrongly blocked")
	}
}

func TestStreamCapacityDisabledIsNoop(t *testing.T) {
	// A nil controller admits everything with a no-op release (dev default off).
	old := streamCapacity
	streamCapacity = nil
	t.Cleanup(func() { streamCapacity = old })
	rel, ok := acquireStreamSlot(nil, nil)
	if !ok || rel == nil {
		t.Fatal("nil capacity should admit with a no-op release")
	}
	rel()
}
