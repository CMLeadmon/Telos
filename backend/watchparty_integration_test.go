package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func wpFixture(t *testing.T) (*pgxpool.Pool, *redis.Client, string, string) {
	t.Helper()
	f := withFixture(t)
	oldDB, oldRedis, oldClock := dbPool, redisClient, wpClock
	dbPool = f.DB
	redisClient = f.Redis
	t.Cleanup(func() { dbPool, redisClient, wpClock = oldDB, oldRedis, oldClock })
	mk := func(n string) string {
		var id string
		f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash) VALUES ($1,'x') RETURNING id::text`, n).Scan(&id)
		return id
	}
	return f.DB, f.Redis, mk("host"), mk("guest")
}

// fixedClock installs a mutable clock and returns advance/now helpers.
func fixedClock(start time.Time) (advance func(time.Duration), now func() time.Time) {
	var mu sync.Mutex
	cur := start
	wpClock = func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	return func(d time.Duration) { mu.Lock(); cur = cur.Add(d); mu.Unlock() }, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
}

func TestWatchPartyCreateAndControl(t *testing.T) {
	_, _, host, _ := wpFixture(t)
	fixedClock(time.Unix(1_700_000_000, 0))
	ctx := context.Background()

	p, err := CreateParty(ctx, host, "movie1", "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st, _ := GetPartyState(ctx, p.ID)
	if st.Version != 1 || st.Action != "paused" {
		t.Fatalf("initial state %+v, want v1/paused", st)
	}
	// Host plays at the current version.
	next, err := ApplyControl(ctx, p.ID, host, WatchPartyControlInput{Action: "play", PositionSeconds: 12, PlaybackRate: 1, ExpectedVersion: 1})
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if next.Version != 2 || next.Action != "playing" {
		t.Fatalf("after play %+v, want v2/playing", next)
	}
	// A stale expected version is rejected.
	if _, err := ApplyControl(ctx, p.ID, host, WatchPartyControlInput{Action: "pause", PositionSeconds: 12, ExpectedVersion: 1}); err != errWPStaleVersion {
		t.Fatalf("stale version = %v, want stale", err)
	}
	// A non-host cannot control.
	if _, err := ApplyControl(ctx, p.ID, "someone-else", WatchPartyControlInput{Action: "pause", ExpectedVersion: 2}); err != errWPNotHost {
		t.Fatalf("non-host control = %v, want not-host", err)
	}
}

func TestWatchPartyHeartbeatAndLeaseExpiry(t *testing.T) {
	_, _, host, _ := wpFixture(t)
	advance, _ := fixedClock(time.Unix(1_700_000_000, 0))
	ctx := context.Background()
	p, _ := CreateParty(ctx, host, "movie1", "", "")

	// A heartbeat sooner than the interval is rejected.
	advance(2 * time.Second)
	if _, err := RenewHostLease(ctx, p.ID, host); err != errWPHeartbeatFast {
		t.Fatalf("fast heartbeat = %v, want too-frequent", err)
	}
	// After the interval it renews the lease.
	advance(4 * time.Second)
	if _, err := RenewHostLease(ctx, p.ID, host); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !LeaseValid(ctx, p.ID) {
		t.Fatal("lease should be valid after heartbeat")
	}
	// Advancing past the lease window expires it; state auto-pauses.
	ApplyControl(ctx, p.ID, host, WatchPartyControlInput{Action: "play", PositionSeconds: 0, PlaybackRate: 1, ExpectedVersion: 1})
	advance(wpLeaseWindow + time.Second)
	if LeaseValid(ctx, p.ID) {
		t.Fatal("lease should be expired")
	}
	st, _ := GetPartyState(ctx, p.ID)
	if st.Action != "paused" {
		t.Fatalf("expired lease should pause, got %q", st.Action)
	}
	// The old host cannot control after expiry.
	if _, err := ApplyControl(ctx, p.ID, host, WatchPartyControlInput{Action: "play", PositionSeconds: 0, PlaybackRate: 1, ExpectedVersion: st.Version}); err != errWPLeaseExpired {
		t.Fatalf("control after expiry = %v, want lease-expired", err)
	}
}

func TestWatchPartySuccessorClaimOnly(t *testing.T) {
	_, _, host, guest := wpFixture(t)
	advance, _ := fixedClock(time.Unix(1_700_000_000, 0))
	ctx := context.Background()
	p, _ := CreateParty(ctx, host, "movie1", "", "")
	RespondInviteless(ctx, t, p.ID, guest)

	// Host offers the guest as successor; guest accepts.
	if err := OfferHost(ctx, p.ID, host, guest); err != nil {
		t.Fatalf("offer: %v", err)
	}
	if err := AcceptHostOffer(ctx, p.ID, guest); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// While the lease is valid, no one can claim.
	if err := ClaimHostLease(ctx, p.ID, guest); err != errWPLeaseExpired {
		t.Fatalf("claim before expiry = %v, want not-yet", err)
	}
	// After expiry, an arbitrary member cannot claim...
	advance(wpLeaseWindow + time.Second)
	if err := ClaimHostLease(ctx, p.ID, "arbitrary-user"); err != errWPNotSuccessor {
		t.Fatalf("arbitrary claim = %v, want not-successor", err)
	}
	// ...only the accepted successor can.
	if err := ClaimHostLease(ctx, p.ID, guest); err != nil {
		t.Fatalf("successor claim: %v", err)
	}
	p2, _ := GetParty(ctx, p.ID)
	if p2.HostID != guest || p2.HostGeneration != p.HostGeneration+1 {
		t.Fatalf("after claim host=%s gen=%d, want guest/gen+1", p2.HostID, p2.HostGeneration)
	}
}

// RespondInviteless force-adds a member without an invitation (test helper).
func RespondInviteless(ctx context.Context, t *testing.T, partyID, userID string) {
	t.Helper()
	if _, err := dbPool.Exec(ctx, `INSERT INTO watch_party_members (party_id,user_id,state) VALUES ($1,$2::uuid,'joined') ON CONFLICT DO NOTHING`, partyID, userID); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

func TestWatchPartyRedisRecovery(t *testing.T) {
	_, rdb, host, guest := wpFixture(t)
	fixedClock(time.Unix(1_700_000_000, 0))
	ctx := context.Background()
	p, _ := CreateParty(ctx, host, "movie1", "", "")
	OfferHost(ctx, p.ID, host, guest)
	AcceptHostOffer(ctx, p.ID, guest)

	// Simulate Redis loss: flush the party's volatile keys.
	rdb.Del(ctx, wpStateKey(p.ID), wpLeaseKey(p.ID), wpBeatKey(p.ID))

	// Durable identity + successor consent survive.
	got, err := GetParty(ctx, p.ID)
	if err != nil || got.ID != p.ID {
		t.Fatalf("durable party lost after redis loss: %v", err)
	}
	if sid, ok := acceptedSuccessor(ctx, p.ID, p.HostGeneration); !ok || sid != guest {
		t.Fatal("successor consent lost after redis loss")
	}
}

func TestDeleteAccountWatchParties(t *testing.T) {
	db, _, host, guest := wpFixture(t)
	ctx := context.Background()
	p, _ := CreateParty(ctx, host, "movie1", "", "")
	RespondInviteless(ctx, t, p.ID, guest)

	if _, err := DeleteAccount(ctx, host); err != nil {
		t.Fatalf("delete host: %v", err)
	}
	var ended *time.Time
	db.QueryRow(ctx, `SELECT ended_at FROM watch_parties WHERE id=$1`, p.ID).Scan(&ended)
	if ended == nil {
		t.Fatal("host deletion did not end the party")
	}
	var members int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM watch_party_members WHERE user_id=$1`, host).Scan(&members)
	if members != 0 {
		t.Fatalf("host membership left behind: %d", members)
	}
}
