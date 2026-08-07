package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestDeviceRegistrationEndpoint(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed test user and active session cookie token
	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('reguser','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}
	rawSessionToken := "test-session-token-reg-123"
	sessionHash := sha256Hex(rawSessionToken)
	_, err = f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sessionHash, userID)
	if err != nil {
		t.Fatalf("failed to insert test session: %v", err)
	}

	// Positive test: Register device with valid cookie
	body := `{"deviceName":"My Phone","platform":"android","clientVersion":"2.1.0"}`
	r := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSessionToken})
	rec := httptest.NewRecorder()

	handleRegisterDevice(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRegisterDevice returned code %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		DeviceID        string `json:"deviceId"`
		RefreshToken    string `json:"refreshToken"`
		AccessToken     string `json:"accessToken"`
		AccessExpiresIn int    `json:"accessExpiresIn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.DeviceID == "" || res.RefreshToken == "" || res.AccessToken == "" {
		t.Fatalf("expected non-empty tokens and deviceId, got %+v", res)
	}
	if res.AccessExpiresIn != 900 {
		t.Errorf("expected accessExpiresIn to be 900, got %d", res.AccessExpiresIn)
	}

	// Negative test 1: Unauthenticated request (no session token)
	rUnauth := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(body))
	rUnauth.Header.Set("Content-Type", "application/json")
	recUnauth := httptest.NewRecorder()
	handleRegisterDevice(recUnauth, rUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("negative test failed: expected 401 Unauthorized for unauthenticated registration, got %d", recUnauth.Code)
	}

	// Negative test 2: Missing required payload fields
	badBody := `{"deviceName":"","platform":"android"}`
	rBad := httptest.NewRequest("POST", "/api/v1/auth/devices/register", strings.NewReader(badBody))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSessionToken})
	recBad := httptest.NewRecorder()
	handleRegisterDevice(recBad, rBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("negative test failed: expected 400 Bad Request for missing fields, got %d", recBad.Code)
	}
}

func TestDeviceRefreshEndpointRotationAndReplay(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('refuser','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}
	deviceID, refresh1, _, err := registerDevice(ctx, userID, "Tablet", "android", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	// Positive test: Refresh token rotation
	body := fmt.Sprintf(`{"refreshToken":%q}`, refresh1)
	r := httptest.NewRequest("POST", "/api/v1/auth/devices/refresh", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleRefreshDevice(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRefreshDevice returned code %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		RefreshToken    string `json:"refreshToken"`
		AccessToken     string `json:"accessToken"`
		AccessExpiresIn int    `json:"accessExpiresIn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal refresh response: %v", err)
	}
	if res.RefreshToken == "" || res.RefreshToken == refresh1 {
		t.Fatalf("expected new rotated refresh token, got %q", res.RefreshToken)
	}

	// Negative test 1: Replayed refresh token MUST return 401 and trigger device revocation
	rReplay := httptest.NewRequest("POST", "/api/v1/auth/devices/refresh", strings.NewReader(body))
	rReplay.Header.Set("Content-Type", "application/json")
	recReplay := httptest.NewRecorder()

	handleRefreshDevice(recReplay, rReplay)

	if recReplay.Code != http.StatusUnauthorized {
		t.Fatalf("negative test failed: expected 401 Unauthorized for replayed refresh token, got %d", recReplay.Code)
	}

	// Verify device is now revoked in database
	var revokedAt *time.Time
	err = f.DB.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceID).Scan(&revokedAt)
	if err != nil || revokedAt == nil {
		t.Fatalf("negative test failed: device was not revoked after refresh token replay attack (revokedAt=%v, err=%v)", revokedAt, err)
	}
}

func TestGetAuthenticatedUserBearerBranch(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('beareruser','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	rawBearerToken := "test-bearer-token-val-456"
	bearerHash := sha256Hex(rawBearerToken)
	_, err = f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')`, bearerHash, userID)
	if err != nil {
		t.Fatalf("failed to insert bearer session: %v", err)
	}

	// Positive test: Bearer header authentication
	rBearer := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rBearer.Header.Set("Authorization", "Bearer "+rawBearerToken)
	uc, err := getAuthenticatedUser(rBearer)
	if err != nil {
		t.Fatalf("getAuthenticatedUser failed for valid Bearer token: %v", err)
	}
	if uc.ID != userID {
		t.Fatalf("user ID mismatch: got %s, want %s", uc.ID, userID)
	}

	// Negative test 1: Expired bearer token
	rawExpToken := "test-expired-bearer-token-789"
	expHash := sha256Hex(rawExpToken)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() - INTERVAL '1 minute')`, expHash, userID)
	rExp := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rExp.Header.Set("Authorization", "Bearer "+rawExpToken)
	_, err = getAuthenticatedUser(rExp)
	if err == nil {
		t.Fatalf("negative test failed: expected error for expired bearer token")
	}

	// Negative test 2: Malformed Bearer header
	rBad := httptest.NewRequest("GET", "/api/v1/users/me", nil)
	rBad.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	_, err = getAuthenticatedUser(rBad)
	if err == nil {
		t.Fatalf("negative test failed: expected error for malformed auth header")
	}
}

func TestListAndRevokeDevicesEndpoint(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('user1','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	devID, _, _, err := registerDevice(ctx, userID, "Phone", "ios", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	rawSess := "sess-user1-list-revoke"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sha256Hex(rawSess), userID)

	// GET /api/v1/users/me/devices
	rList := httptest.NewRequest("GET", "/api/v1/users/me/devices", nil)
	rList.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSess})
	recList := httptest.NewRecorder()
	handleListDevices(recList, rList)

	if recList.Code != http.StatusOK {
		t.Fatalf("handleListDevices returned code %d, want 200", recList.Code)
	}

	var devs []DeviceSummary
	if err := json.Unmarshal(recList.Body.Bytes(), &devs); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(devs) != 1 || devs[0].ID != devID {
		t.Fatalf("unexpected devices list: %+v", devs)
	}

	// DELETE /api/v1/users/me/devices/{id}
	rDel := httptest.NewRequest("DELETE", "/api/v1/users/me/devices/"+devID, nil)
	rDel.SetPathValue("id", devID)
	rDel.AddCookie(&http.Cookie{Name: "telos_session", Value: rawSess})
	recDel := httptest.NewRecorder()
	handleRevokeDevice(recDel, rDel)

	if recDel.Code != http.StatusNoContent {
		t.Fatalf("handleRevokeDevice returned code %d, want 204", recDel.Code)
	}
}

func TestCrossUserDeviceAuthorization(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed User A and User B
	var userA, userB string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('usera','x') RETURNING id`).Scan(&userA)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('userb','x') RETURNING id`).Scan(&userB)

	// User B registers a device
	deviceB, _, _, err := registerDevice(ctx, userB, "UserB Laptop", "macos", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	// User A session token
	sessA := "sess-user-a-token-999"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '1 hour')`, sha256Hex(sessA), userA)

	// Requirement 1: listDevices for user A NEVER returns user B's device
	rListA := httptest.NewRequest("GET", "/api/v1/users/me/devices", nil)
	rListA.AddCookie(&http.Cookie{Name: "telos_session", Value: sessA})
	recListA := httptest.NewRecorder()
	handleListDevices(recListA, rListA)

	var devsA []DeviceSummary
	json.Unmarshal(recListA.Body.Bytes(), &devsA)
	for _, d := range devsA {
		if d.ID == deviceB {
			t.Fatalf("SECURITY FAILURE: listDevices for User A returned User B's device %s", deviceB)
		}
	}

	// Requirement 2: DELETE /api/v1/users/me/devices/{id} with User A's credentials and User B's device ID returns 404 (NOT 403)
	rDelCross := httptest.NewRequest("DELETE", "/api/v1/users/me/devices/"+deviceB, nil)
	rDelCross.SetPathValue("id", deviceB)
	rDelCross.AddCookie(&http.Cookie{Name: "telos_session", Value: sessA})
	recDelCross := httptest.NewRecorder()
	handleRevokeDevice(recDelCross, rDelCross)

	if recDelCross.Code != http.StatusNotFound {
		t.Fatalf("SECURITY FAILURE: cross-user device revocation returned code %d, want 404 Not Found to prevent registry leakage", recDelCross.Code)
	}

	// Requirement 3: After that attempt, User B's device is STILL live and usable (revoked_at IS NULL)
	var revokedAt *time.Time
	f.DB.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceB).Scan(&revokedAt)
	if revokedAt != nil {
		t.Fatalf("SECURITY FAILURE: User B's device was revoked by User A's unauthorized request")
	}
}

func TestWSTicketIssuanceAndUpgrade(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsticketuser','x') RETURNING id`).Scan(&userID)
	_, _, _, err := registerDevice(ctx, userID, "PC", "linux", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	rawBearer := "bearer-ws-ticket-test-123"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')`, sha256Hex(rawBearer), userID)

	// POST /api/v1/auth/ws-ticket
	rTicket := httptest.NewRequest("POST", "/api/v1/auth/ws-ticket", nil)
	rTicket.Header.Set("Authorization", "Bearer "+rawBearer)
	recTicket := httptest.NewRecorder()

	handleWSTicket(recTicket, rTicket)

	if recTicket.Code != http.StatusOK {
		t.Fatalf("handleWSTicket returned code %d, want 200; body: %s", recTicket.Code, recTicket.Body.String())
	}

	var res struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	json.Unmarshal(recTicket.Body.Bytes(), &res)
	if res.Ticket == "" || res.ExpiresIn != 30 {
		t.Fatalf("invalid ticket response: %+v", res)
	}

	// Consume ticket
	uID, dID, err := consumeWSTicket(ctx, res.Ticket)
	if err != nil || uID != userID {
		t.Fatalf("consumeWSTicket failed: got user=%s dev=%s err=%v", uID, dID, err)
	}
}

func TestCrossUserWSTicketIsolation(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userA, userB string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsusera','x') RETURNING id`).Scan(&userA)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('wsuserb','x') RETURNING id`).Scan(&userB)

	devA, _, _, err := registerDevice(ctx, userA, "UserA Phone", "ios", "1.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}

	// Issue WS ticket for User A
	ticketA, err := issueWSTicket(ctx, userA, devA)
	if err != nil {
		t.Fatalf("issueWSTicket failed: %v", err)
	}

	// Consume ticket
	claimedUser, claimedDev, err := consumeWSTicket(ctx, ticketA)
	if err != nil {
		t.Fatalf("consumeWSTicket failed: %v", err)
	}

	// Requirement: Ticket issued for User A MUST NOT yield User B identity
	if claimedUser == userB {
		t.Fatalf("SECURITY FAILURE: WS ticket issued to User A resolved to User B identity")
	}
	if claimedUser != userA || claimedDev != devA {
		t.Fatalf("unexpected ticket resolution: got user=%s dev=%s, want userA=%s devA=%s", claimedUser, claimedDev, userA, devA)
	}
}

