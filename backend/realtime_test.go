package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

type fakeConn struct {
	closed atomic.Int32
}

func (f *fakeConn) CloseWithCode(int, string) { f.closed.Add(1) }

func TestSessionRegistryRevokeUser(t *testing.T) {
	reg := newSessionRegistry()
	c1, c2 := &fakeConn{}, &fakeConn{}
	rel1, ok1 := reg.Register("sessA", "user1", c1)
	_, ok2 := reg.Register("sessB", "user1", c2)
	if !ok1 || !ok2 {
		t.Fatal("registration failed")
	}
	defer rel1()

	reg.RevokeUser("user1")
	if c1.closed.Load() == 0 || c2.closed.Load() == 0 {
		t.Fatal("RevokeUser did not close both of the user's sockets")
	}
}

func TestSessionRegistryRevokeSession(t *testing.T) {
	reg := newSessionRegistry()
	c1, c2 := &fakeConn{}, &fakeConn{}
	reg.Register("sessA", "user1", c1)
	reg.Register("sessB", "user1", c2)

	reg.RevokeSession("sessA")
	if c1.closed.Load() == 0 {
		t.Fatal("RevokeSession did not close the target session socket")
	}
	if c2.closed.Load() != 0 {
		t.Fatal("RevokeSession closed an unrelated session socket")
	}
}

func TestSessionRegistryPerUserCap(t *testing.T) {
	reg := newSessionRegistry()
	for i := 0; i < maxSocketsPerUser; i++ {
		if _, ok := reg.Register("s", "user1", &fakeConn{}); !ok {
			t.Fatalf("registration %d unexpectedly rejected", i)
		}
	}
	if _, ok := reg.Register("s", "user1", &fakeConn{}); ok {
		t.Fatalf("registration beyond cap %d was accepted", maxSocketsPerUser)
	}
	// A different user is unaffected.
	if _, ok := reg.Register("s", "user2", &fakeConn{}); !ok {
		t.Fatal("cap wrongly applied across users")
	}
}

func TestSessionRegistryReleaseFreesSlot(t *testing.T) {
	reg := newSessionRegistry()
	rels := make([]func(), 0)
	for i := 0; i < maxSocketsPerUser; i++ {
		rel, ok := reg.Register("s", "user1", &fakeConn{})
		if !ok {
			t.Fatalf("registration %d rejected", i)
		}
		rels = append(rels, rel)
	}
	rels[0]() // release one slot
	if _, ok := reg.Register("s", "user1", &fakeConn{}); !ok {
		t.Fatal("slot was not freed after release")
	}
}

func TestSessionRegistryConcurrentSafe(t *testing.T) {
	reg := newSessionRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rel, ok := reg.Register("s", "u", &fakeConn{})
			if ok {
				reg.RevokeUser("u")
				rel()
			}
		}(i)
	}
	wg.Wait()
}
