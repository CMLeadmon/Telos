package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"telos-core/testutil"
)

// --- canonical username (pure) -----------------------------------------------

func TestCanonicalUsername(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Alice", "alice", true},
		{"bob_the.builder-2", "bob_the.builder-2", true},
		{"ab", "", false},                    // too short
		{strings.Repeat("a", 33), "", false}, // too long
		{"_leading", "", false},              // must start alphanumeric
		{" alice", "", false},                // leading space
		{"alice ", "", false},                // trailing space
		{"al ice", "", false},                // embedded space
		{"al\tice", "", false},               // tab
		{"ali ce", "", false},                // NBSP (Unicode whitespace)
		{"аlice", "", false},                 // Cyrillic 'а' (non-ASCII)
		{"alice!", "", false},                // illegal punctuation
		{"", "", false},                      // empty
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := canonicalUsername(tc.in)
			if tc.ok && (err != nil || got != tc.want) {
				t.Fatalf("canonicalUsername(%q) = (%q,%v), want (%q,nil)", tc.in, got, err, tc.want)
			}
			if !tc.ok && err == nil {
				t.Fatalf("canonicalUsername(%q) accepted, want rejection", tc.in)
			}
		})
	}
}

// --- secure token random failure ---------------------------------------------

type failingRandom struct{}

func (failingRandom) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

type shortRandom struct{}

func (shortRandom) Read(p []byte) (int, error) { return len(p) - 1, nil }

func TestSecureTokenRejectsRandomFailure(t *testing.T) {
	old := randomSource
	t.Cleanup(func() { randomSource = old })

	randomSource = failingRandom{}
	if _, err := secureToken(32); err == nil {
		t.Fatal("secureToken accepted a failing random source")
	}
	if _, _, err := generateToken(); err == nil {
		t.Fatal("generateToken accepted a failing random source")
	}

	randomSource = shortRandom{}
	if _, err := secureToken(32); err == nil {
		t.Fatal("secureToken accepted a short read")
	}
}

// --- degraded limiter (pure) -------------------------------------------------

func TestLoginLimiterDegraded(t *testing.T) {
	d := newDegradedLimiter()
	ip := netip.MustParseAddr("203.0.113.5")

	// Pair cap is 2; the third reservation for the same pair must fail closed.
	a1, err := d.reserve("user1", ip)
	if err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	a2, err := d.reserve("user1", ip)
	if err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	if _, err := d.reserve("user1", ip); !errors.Is(err, errAuthThrottleUnavailable) {
		t.Fatalf("reserve 3 err = %v, want errAuthThrottleUnavailable", err)
	}
	// Completing a reservation as failure keeps pressure (fails count), so the
	// pair stays saturated.
	_ = a1.complete(LoginFailure)
	_ = a2.complete(LoginFailure)
	if _, err := d.reserve("user1", ip); !errors.Is(err, errAuthThrottleUnavailable) {
		t.Fatalf("post-failure reserve err = %v, want saturated", err)
	}
}

// --- integration: concurrency & limiter --------------------------------------

func withFixture(t *testing.T) *testutil.Fixture {
	t.Helper()
	f := testutil.Setup(t)
	oldDB, oldRedis, oldLimiter := dbPool, redisClient, loginLimiter
	dbPool = f.DB
	redisClient = f.Redis
	loginLimiter = newLoginLimiter(f.Redis)
	t.Cleanup(func() {
		dbPool, redisClient, loginLimiter = oldDB, oldRedis, oldLimiter
	})
	return f
}

func bootstrapReq(t *testing.T, username string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":"a-valid-long-password","token":"test-bootstrap-token-1234567890"}`, username)
	r := httptest.NewRequest("POST", "/api/v1/auth/bootstrap", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleBootstrap(rec, r)
	return rec
}

func TestBootstrapConcurrentYieldsOneOwner(t *testing.T) {
	f := withFixture(t)
	t.Setenv("TELOS_BOOTSTRAP_TOKEN", "test-bootstrap-token-1234567890")

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	created := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := bootstrapReq(t, fmt.Sprintf("owner%d", i))
			if rec.Code == http.StatusCreated {
				mu.Lock()
				created++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if created != 1 {
		t.Fatalf("bootstrap created %d owners, want 1", created)
	}
	var owners, users int
	f.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_roles WHERE role_id='Owner'`).Scan(&owners)
	f.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&users)
	if owners != 1 || users != 1 {
		t.Fatalf("owners=%d users=%d, want 1/1", owners, users)
	}
}

func TestInviteAcceptanceConcurrentYieldsOneUser(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed an Owner and one invite.
	var ownerID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('owner','x') RETURNING id`).Scan(&ownerID)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Owner')`, ownerID)
	// token_hash = sha256("shared-invite"); compute in Go.
	rawToken := "shared-invite-token-value"
	th := sha256Hex(rawToken)
	f.DB.Exec(ctx, `INSERT INTO invites (token_hash, creator_id, expires_at, role_id) VALUES ($1,$2, NOW() + INTERVAL '1 hour', 'Member')`, th, ownerID)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body := fmt.Sprintf(`{"token":%q,"username":"member%d","password":"a-valid-long-password"}`, rawToken, i)
			r := httptest.NewRequest("POST", "/api/v1/auth/invites/accept", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handleAcceptInvite(rec, r)
			if rec.Code == http.StatusCreated {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if accepted != 1 {
		t.Fatalf("invite accepted %d times, want 1", accepted)
	}
	var members int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username LIKE 'member%'`).Scan(&members)
	if members != 1 {
		t.Fatalf("created %d members, want 1", members)
	}
}

func TestLastOwnerConcurrentCannotReachZero(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Two Owners; two concurrent deletes (one each). Exactly one must survive.
	var a, b string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('owner_a','x') RETURNING id`).Scan(&a)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('owner_b','x') RETURNING id`).Scan(&b)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Owner'),($2,'Owner')`, a, b)

	actor := &UserContext{ID: "super", Roles: []string{"Owner"}, Permissions: []string{"manage_members"}}
	del := func(target string) {
		r := httptest.NewRequest("DELETE", "/api/v1/admin/users/"+target, nil)
		r.SetPathValue("id", target)
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, actor))
		handleAdminDeleteUser(httptest.NewRecorder(), r)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range []string{a, b} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			del(id)
		}(id)
	}
	close(start)
	wg.Wait()

	var owners int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM user_roles WHERE role_id='Owner'`).Scan(&owners)
	if owners < 1 {
		t.Fatalf("owners=%d, the last Owner was removed", owners)
	}
}

func TestLoginLimiterConcurrentRespectsCaps(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	limiter := newLoginLimiter(f.Redis)
	ip := netip.MustParseAddr("198.51.100.7")

	// 100 concurrent reservations for the same IP; the IP cap is 20, so at
	// most 20 may be live at once. Reserve-only (never complete) so live
	// reservations accumulate against the cap.
	const n = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	reserved := 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Distinct usernames so only the IP scope is the binding cap.
			if _, err := limiter.Reserve(ctx, fmt.Sprintf("u%d", i), ip); err == nil {
				mu.Lock()
				reserved++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if reserved > limitIP {
		t.Fatalf("reserved %d slots for one IP, cap is %d", reserved, limitIP)
	}
	if reserved == 0 {
		t.Fatal("no reservations succeeded")
	}
}
