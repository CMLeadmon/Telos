package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the real production handler chain against real PostgreSQL and
// Redis, with Jellyfin replaced by a local upstream. They pin the capability model
// in media_authority.go: a media URL is a bearer credential whose authority is
// re-checked on every request.

const (
	capVideoID       = "00000000-0000-4000-8000-0000000000a1"
	capVideoUpstream = "movie-a1"
	capAudioID       = "00000000-0000-4000-8000-0000000000a2"
	capAudioUpstream = "track-a2"
	capAdminToken    = "admin-token-canary-8f31c2"
	capOrigin        = "https://community.example.org"
)

type mediaFixture struct {
	t       *testing.T
	handler http.Handler
	userDB  *mediaDB

	mu        sync.Mutex
	active    bool
	available bool
	library   string
	hits      []string
}

type mediaDB struct {
	exec func(sql string, args ...any)
	row  func(sql string, args ...any) interface{ Scan(dest ...any) error }
}

func withMediaFixture(t *testing.T) *mediaFixture {
	t.Helper()
	f := withFixture(t)
	handler := productionChain(t)
	m := &mediaFixture{t: t, handler: handler, active: true, available: true, library: "lib-allowed"}
	m.userDB = &mediaDB{
		exec: func(sql string, args ...any) {
			t.Helper()
			if _, err := f.DB.Exec(context.Background(), sql, args...); err != nil {
				t.Fatalf("exec %q: %v", sql, err)
			}
		},
		row: func(sql string, args ...any) interface{ Scan(dest ...any) error } {
			return f.DB.QueryRow(context.Background(), sql, args...)
		},
	}

	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch rawID {
		case capVideoID, capVideoUpstream:
			return CatalogResolution{ID: capVideoID, Surface: surface, Kind: "video", Provider: ProviderJellyfin,
				UpstreamID: capVideoUpstream, LibraryID: m.library, Active: m.active, Available: m.available}, nil
		case capAudioID, capAudioUpstream:
			return CatalogResolution{ID: capAudioID, Surface: surface, Kind: "audio", Provider: ProviderJellyfin,
				UpstreamID: capAudioUpstream, LibraryID: m.library, Active: m.active, Available: m.available}, nil
		}
		return CatalogResolution{}, errCatalogNotFound
	}
	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		capVideoUpstream: {MediaType: "Video", AncestorIDs: []string{"lib-allowed"}},
		capAudioUpstream: {MediaType: "Audio", AncestorIDs: []string{"lib-allowed"}},
	}}, []string{"lib-allowed", "lib-removed"})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.hits = append(m.hits, r.Method+" "+r.URL.Path)
		m.mu.Unlock()
		if r.Header.Get("X-Emby-Token") != capAdminToken && r.URL.Path != "/Users" {
			// The admin token must be injected server-side on every upstream call.
			if !strings.HasPrefix(r.URL.Path, "/Users") {
				t.Errorf("upstream call without the server-side admin token: %s", r.URL.Path)
			}
		}
		switch {
		case r.URL.Path == "/Users":
			json.NewEncoder(w).Encode([]map[string]string{{"Id": "user-1", "Name": "admin"}})
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			json.NewEncoder(w).Encode(map[string]string{"PlaySessionId": "play-a1"})
		case r.URL.Path == "/Videos/"+capVideoUpstream+"/main.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nhls1/main/index.m3u8?PlaySessionId=play-a1&api_key=SECRET-UPSTREAM\n")
		case r.URL.Path == "/Videos/"+capVideoUpstream+"/hls1/main/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:6.0,\nhls1/main/0.ts\n#EXTINF:6.0,\nhls1/main/1.ts\n")
		case strings.HasPrefix(r.URL.Path, "/Videos/"+capVideoUpstream+"/hls1/main/") && strings.HasSuffix(r.URL.Path, ".ts"):
			http.ServeContent(w, r, "seg.ts", time.Time{}, bytes.NewReader(bytes.Repeat([]byte("S"), 100)))
		case r.URL.Path == "/Audio/"+capAudioUpstream+"/stream":
			http.ServeContent(w, r, "a.mp3", time.Time{}, bytes.NewReader(bytes.Repeat([]byte("A"), 200)))
		default:
			http.NotFound(w, r)
		}
	}))
	oldBase, oldRedis, oldHost := jellyfinBaseURL, redisClient, hlsOriginHost
	jellyfinBaseURL, redisClient = upstream.URL, f.Redis
	hlsOriginHost = strings.TrimPrefix(upstream.URL, "http://")
	t.Setenv("JELLYFIN_ADMIN_TOKEN", capAdminToken)
	oldKey := hlsSigningKey
	hlsSigningKey = []byte("media-capability-test-signing-key-32b!")
	t.Cleanup(func() {
		upstream.Close()
		jellyfinBaseURL, redisClient, hlsOriginHost = oldBase, oldRedis, oldHost
		hlsSigningKey = oldKey
		resolveCatalogIdentity = oldResolve
		jellyfinAuthorizer = oldAuthorizer
	})
	return m
}

func (m *mediaFixture) setItem(active, available bool, library string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active, m.available, m.library = active, available, library
}

func (m *mediaFixture) upstreamHits() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.hits...)
}

// principal is a signed-in caller of either kind.
type principal struct {
	userID   string
	cookie   string // raw session cookie value (browser)
	bearer   string // access token (device)
	deviceID string
	refresh  string
	origin   string
}

func (p principal) apply(r *http.Request) {
	r.Header.Set("Origin", p.origin)
	if p.cookie != "" {
		r.AddCookie(&http.Cookie{Name: "telos_session", Value: p.cookie})
	}
	if p.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+p.bearer)
	}
}

var userCounter int

func (m *mediaFixture) seedUser(role string) string {
	m.t.Helper()
	userCounter++
	var id string
	if err := m.userDB.row(`INSERT INTO users (username, password_hash) VALUES ($1,'x') RETURNING id`, fmt.Sprintf("capuser%d", userCounter)).Scan(&id); err != nil {
		m.t.Fatalf("seed user: %v", err)
	}
	if role != "" {
		m.userDB.exec(`INSERT INTO user_roles (user_id, role_id) VALUES ($1,$2)`, id, role)
	}
	return id
}

func (m *mediaFixture) browser(role string, ttl string) principal {
	m.t.Helper()
	id := m.seedUser(role)
	raw := fmt.Sprintf("browser-session-cookie-%d-secret", userCounter)
	m.userDB.exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2, NOW() + $3::interval)`, sha256Hex(raw), id, ttl)
	return principal{userID: id, cookie: raw, origin: capOrigin}
}

func (m *mediaFixture) desktop(role string) principal {
	m.t.Helper()
	id := m.seedUser(role)
	deviceID, refresh, access, err := registerDevice(context.Background(), id, "Desktop", "linux", "0.1.0")
	if err != nil {
		m.t.Fatalf("registerDevice: %v", err)
	}
	return principal{userID: id, bearer: access, deviceID: deviceID, refresh: refresh, origin: "tauri://localhost"}
}

func (m *mediaFixture) call(method, path string, p *principal, body string) *httptest.ResponseRecorder {
	m.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		p.apply(r)
	}
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, r)
	return rec
}

func (m *mediaFixture) spend(path string, headers map[string]string) *httptest.ResponseRecorder {
	m.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, r)
	return rec
}

type mintResult struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expiresIn"`
}

func (m *mediaFixture) mintVideo(p principal) (mintResult, *httptest.ResponseRecorder) {
	m.t.Helper()
	rec := m.call(http.MethodPost, "/api/v1/stream/video/"+capVideoID+"/playback-locator", &p, "")
	var out mintResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec
}

func (m *mediaFixture) mintAudio(p principal) (mintResult, *httptest.ResponseRecorder) {
	m.t.Helper()
	rec := m.call(http.MethodPost, "/api/v1/stream/audio/"+capAudioID+"/stream-ticket", &p, "")
	var out mintResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec
}

func locatorFromURL(t *testing.T, url string) hlsLocator {
	t.Helper()
	loc, err := verifyHLSLocator(strings.TrimPrefix(url, "/api/v1/hls/"), time.Now())
	if err != nil {
		t.Fatalf("minted URL %q is not a valid locator: %v", url, err)
	}
	return loc
}

// ---------------------------------------------------------------------------

func TestMediaLocatorMintRequiresAuthenticationAndPermission(t *testing.T) {
	m := withMediaFixture(t)
	if rec := m.call(http.MethodPost, "/api/v1/stream/video/"+capVideoID+"/playback-locator", &principal{origin: capOrigin}, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous video mint = %d, want 401", rec.Code)
	}
	if rec := m.call(http.MethodPost, "/api/v1/stream/audio/"+capAudioID+"/stream-ticket", &principal{origin: capOrigin}, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous audio mint = %d, want 401", rec.Code)
	}
	noRole := m.browser("", "1 hour")
	if _, rec := m.mintVideo(noRole); rec.Code != http.StatusForbidden {
		t.Fatalf("a member without view_media minted a video locator: %d", rec.Code)
	}
	if _, rec := m.mintAudio(noRole); rec.Code != http.StatusForbidden {
		t.Fatalf("a member without view_media minted an audio ticket: %d", rec.Code)
	}
	for name, p := range map[string]principal{"cookie": m.browser("Member", "1 hour"), "bearer": m.desktop("Member")} {
		if out, rec := m.mintVideo(p); rec.Code != http.StatusOK || !strings.HasPrefix(out.URL, "/api/v1/hls/") {
			t.Fatalf("%s video mint = %d %s", name, rec.Code, rec.Body.String())
		}
		if out, rec := m.mintAudio(p); rec.Code != http.StatusOK || !strings.HasPrefix(out.URL, "/api/v1/stream/audio-ticket/") {
			t.Fatalf("%s audio mint = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := m.call(http.MethodPost, "/api/v1/stream/video/not-an-id!/playback-locator", ptr(m.desktop("Member")), ""); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("a malformed item id was accepted: %d", rec.Code)
	}
}

func ptr[T any](v T) *T { return &v }

func TestMediaLocatorClaimsNameTheAuthorityNotTheCredential(t *testing.T) {
	m := withMediaFixture(t)
	desktop := m.desktop("Member")
	out, _ := m.mintVideo(desktop)
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(strings.TrimPrefix(out.URL, "/api/v1/hls/"), ".")[0])
	for _, secret := range []string{desktop.bearer, desktop.refresh} {
		if strings.Contains(string(body), secret) || strings.Contains(out.URL, secret) {
			t.Fatalf("the locator carries a raw credential: %s", body)
		}
	}
	loc := locatorFromURL(t, out.URL)
	if loc.Auth != (mediaAuthority{Kind: authorityDevice, ID: desktop.deviceID}) || loc.User != desktop.userID || loc.Item != capVideoUpstream {
		t.Fatalf("claims = %+v", loc)
	}

	browser := m.browser("Member", "1 hour")
	out, _ = m.mintVideo(browser)
	loc = locatorFromURL(t, out.URL)
	if loc.Auth.Kind != authoritySession || loc.Auth.ID != sha256Hex(browser.cookie) {
		t.Fatalf("browser claims = %+v", loc)
	}
	if strings.Contains(out.URL, browser.cookie) {
		t.Fatal("the locator carries the raw session cookie")
	}
}

func TestMediaLocatorLifetimeIsTwelveHoursAndNeverPastTheAuthority(t *testing.T) {
	m := withMediaFixture(t)
	out, _ := m.mintVideo(m.desktop("Member"))
	if out.ExpiresIn < 12*3600-5 || out.ExpiresIn > 12*3600 {
		t.Fatalf("device expiresIn = %d, want ~43200", out.ExpiresIn)
	}
	loc := locatorFromURL(t, out.URL)
	if got := time.Until(time.Unix(loc.Exp, 0)); got > 12*time.Hour || got < 12*time.Hour-time.Minute {
		t.Fatalf("locator exp in %s", got)
	}

	short := m.browser("Member", "1 hour")
	out, rec := m.mintVideo(short)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d", rec.Code)
	}
	if out.ExpiresIn > 3600 || out.ExpiresIn < 3590 {
		t.Fatalf("a 1-hour session produced expiresIn = %d", out.ExpiresIn)
	}
	var sessionEnd time.Time
	if err := m.userDB.row(`SELECT expires_at FROM sessions WHERE token_hash=$1`, sha256Hex(short.cookie)).Scan(&sessionEnd); err != nil {
		t.Fatal(err)
	}
	if loc := locatorFromURL(t, out.URL); loc.Exp > sessionEnd.Unix() {
		t.Fatalf("the locator outlives its session: %d > %d", loc.Exp, sessionEnd.Unix())
	}

	audio, _ := m.mintAudio(m.desktop("Member"))
	if audio.ExpiresIn < 12*3600-5 {
		t.Fatalf("audio expiresIn = %d", audio.ExpiresIn)
	}
}

func TestMediaLocatorSpendNeedsNoCredentialAndHonoursRangeAndHead(t *testing.T) {
	m := withMediaFixture(t)
	out, _ := m.mintVideo(m.desktop("Member"))

	// The first locator is a master manifest; it is rewritten to nested locators.
	master := m.spend(out.URL, nil)
	if master.Code != http.StatusOK || !strings.Contains(master.Body.String(), "/api/v1/hls/") {
		t.Fatalf("master = %d %s", master.Code, master.Body.String())
	}
	if strings.Contains(master.Body.String(), "SECRET-UPSTREAM") || strings.Contains(master.Body.String(), capAdminToken) {
		t.Fatalf("an upstream token leaked into the manifest: %s", master.Body.String())
	}
	variant := lastLine(master.Body.String())
	media := m.spend(variant, nil)
	if media.Code != http.StatusOK {
		t.Fatalf("variant = %d %s", media.Code, media.Body.String())
	}
	segments := nonCommentLines(media.Body.String())
	if len(segments) != 2 {
		t.Fatalf("segments = %v", segments)
	}
	full := m.spend(segments[0], nil)
	if full.Code != http.StatusOK || full.Body.Len() != 100 {
		t.Fatalf("segment = %d (%d bytes)", full.Code, full.Body.Len())
	}
	part := m.spend(segments[0], map[string]string{"Range": "bytes=10-19"})
	if part.Code != http.StatusPartialContent || part.Body.Len() != 10 || part.Header().Get("Content-Range") != "bytes 10-19/100" {
		t.Fatalf("range = %d %q (%d bytes)", part.Code, part.Header().Get("Content-Range"), part.Body.Len())
	}
	r := httptest.NewRequest(http.MethodHead, segments[1], nil)
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("head = %d (%d bytes)", rec.Code, rec.Body.Len())
	}
	// Spending sent no cookie or header; the response must forbid caching and
	// referrer leakage because the URL is a credential.
	for _, rec := range []*httptest.ResponseRecorder{master, media, full, part} {
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("capability response headers: Cache-Control=%q Referrer-Policy=%q", rec.Header().Get("Cache-Control"), rec.Header().Get("Referrer-Policy"))
		}
	}
	mint := m.call(http.MethodPost, "/api/v1/stream/video/"+capVideoID+"/playback-locator", ptr(m.desktop("Member")), "")
	if mint.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("mint Cache-Control = %q", mint.Header().Get("Cache-Control"))
	}
}

func lastLine(s string) string {
	lines := nonCommentLines(s)
	return lines[len(lines)-1]
}

func nonCommentLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func TestMediaLocatorRejectsTamperExpiryAndWrongType(t *testing.T) {
	m := withMediaFixture(t)
	desktop := m.desktop("Member")
	out, _ := m.mintVideo(desktop)
	token := strings.TrimPrefix(out.URL, "/api/v1/hls/")

	notFound := func(name, path string) {
		t.Helper()
		before := len(m.upstreamHits())
		if rec := m.spend(path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, rec.Code)
		}
		if after := len(m.upstreamHits()); after != before {
			t.Errorf("%s reached the upstream", name)
		}
	}
	notFound("flipped signature", "/api/v1/hls/"+token[:len(token)-3]+"AAA")
	notFound("edited body", "/api/v1/hls/A"+token[1:])
	notFound("malformed", "/api/v1/hls/not-a-locator")
	notFound("bad base64", "/api/v1/hls/a!b.c!d")

	valid := locatorFromURL(t, out.URL)
	expired := valid
	expired.Exp = time.Now().Add(-time.Second).Unix()
	notFound("expired", "/api/v1/hls/"+signHLSLocator(expired))

	otherUser := valid
	otherUser.User = m.seedUser("Member")
	notFound("user does not own the authority", "/api/v1/hls/"+signHLSLocator(otherUser))

	traversal := valid
	traversal.Res = "../../System/Info"
	notFound("traversal resource", "/api/v1/hls/"+signHLSLocator(traversal))

	wrongAuthority := valid
	wrongAuthority.Auth = mediaAuthority{Kind: authoritySession, ID: valid.Auth.ID}
	notFound("authority kind swapped", "/api/v1/hls/"+signHLSLocator(wrongAuthority))

	badKind := valid
	badKind.Auth.Kind = "root"
	notFound("unknown authority kind", "/api/v1/hls/"+signHLSLocator(badKind))

	// Type confusion in both directions.
	audioToken := strings.TrimPrefix(func() string { r, _ := m.mintAudio(desktop); return r.URL }(), "/api/v1/stream/audio-ticket/")
	notFound("audio ticket as HLS locator", "/api/v1/hls/"+audioToken)
	if rec := m.spend("/api/v1/stream/audio-ticket/"+token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("HLS locator as audio ticket = %d, want 401", rec.Code)
	}
	book := signStreamTicket(streamTicket{Item: capVideoID, Track: -1, User: desktop.userID, Auth: valid.Auth, Exp: valid.Exp})
	notFound("audiobook ticket as HLS locator", "/api/v1/hls/"+book)
	if rec := m.spend("/api/v1/stream/audio-ticket/"+book, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("audiobook ticket as audio ticket = %d, want 401", rec.Code)
	}
}

// The signed claims do not identify the viewer; only the issuer's authority is
// checked. A copied URL therefore keeps working for whoever holds it until the
// authority ends or the URL expires -- this test states that boundary instead of
// pretending to detect copying.
func TestMediaLocatorCopiedURLIsABearerCapability(t *testing.T) {
	m := withMediaFixture(t)
	owner := m.desktop("Member")
	out, _ := m.mintVideo(owner)
	if rec := m.spend(out.URL, map[string]string{"User-Agent": "someone-else", "X-Forwarded-For": "203.0.113.9"}); rec.Code != http.StatusOK {
		t.Fatalf("a copied URL was refused: %d (cross-viewer copy detection is not a promise)", rec.Code)
	}
	if err := revokeDevice(context.Background(), owner.deviceID); err != nil {
		t.Fatal(err)
	}
	if rec := m.spend(out.URL, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a copied URL outlived its issuer: %d", rec.Code)
	}
}

func TestMediaLocatorEndsWhenItsAuthorityOrPermissionEnds(t *testing.T) {
	cases := []struct {
		name    string
		browser bool
		perturb func(m *mediaFixture, p principal)
	}{
		{"browser session revoked (logout)", true, func(m *mediaFixture, p principal) {
			m.userDB.exec(`UPDATE sessions SET revoked_at = NOW() WHERE token_hash=$1`, sha256Hex(p.cookie))
		}},
		{"browser session expired", true, func(m *mediaFixture, p principal) {
			m.userDB.exec(`UPDATE sessions SET created_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 second' WHERE token_hash=$1`, sha256Hex(p.cookie))
		}},
		{"device revoked", false, func(m *mediaFixture, p principal) { _ = revokeDevice(context.Background(), p.deviceID) }},
		{"device has no live refresh credential (expired)", false, func(m *mediaFixture, p principal) {
			m.userDB.exec(`UPDATE device_refresh_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE device_id=$1`, p.deviceID)
		}},
		{"account disabled (device)", false, func(m *mediaFixture, p principal) {
			m.userDB.exec(`UPDATE users SET active=false WHERE id=$1`, p.userID)
		}},
		{"account disabled (browser)", true, func(m *mediaFixture, p principal) {
			m.userDB.exec(`UPDATE users SET active=false WHERE id=$1`, p.userID)
		}},
		{"role removed", false, func(m *mediaFixture, p principal) {
			m.userDB.exec(`DELETE FROM user_roles WHERE user_id=$1`, p.userID)
		}},
		{"item deactivated", false, func(m *mediaFixture, p principal) { m.setItem(false, true, "lib-allowed") }},
		{"source unavailable", false, func(m *mediaFixture, p principal) { m.setItem(true, false, "lib-allowed") }},
		{"library removed from the allowlist", false, func(m *mediaFixture, p principal) { m.setItem(true, true, "lib-not-configured") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := withMediaFixture(t)
			var p principal
			if c.browser {
				p = m.browser("Member", "1 hour")
			} else {
				p = m.desktop("Member")
			}
			video, _ := m.mintVideo(p)
			audio, _ := m.mintAudio(p)
			if rec := m.spend(video.URL, nil); rec.Code != http.StatusOK {
				t.Fatalf("before: video %d", rec.Code)
			}
			if rec := m.spend(audio.URL, nil); rec.Code != http.StatusOK {
				t.Fatalf("before: audio %d %s", rec.Code, rec.Body.String())
			}
			c.perturb(m, p)
			if rec := m.spend(video.URL, nil); rec.Code != http.StatusNotFound {
				t.Errorf("after: video locator still served (%d)", rec.Code)
			}
			if rec := m.spend(audio.URL, nil); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
				t.Errorf("after: audio ticket still served (%d)", rec.Code)
			}
		})
	}
}

// Playback must survive access-token rotation: the authority is the device, not
// the fifteen-minute access session.
func TestMediaLocatorSurvivesAccessTokenRotation(t *testing.T) {
	m := withMediaFixture(t)
	p := m.desktop("Member")
	video, _ := m.mintVideo(p)
	audio, _ := m.mintAudio(p)
	if _, newAccess, err := rotateDeviceToken(context.Background(), p.refresh); err != nil || newAccess == p.bearer {
		t.Fatalf("rotation: %v", err)
	}
	// The old access session is gone (rotation retires it), yet playback continues.
	m.userDB.exec(`UPDATE sessions SET revoked_at = NOW() WHERE token_hash=$1`, sha256Hex(p.bearer))
	if rec := m.spend(video.URL, nil); rec.Code != http.StatusOK {
		t.Fatalf("video after rotation = %d", rec.Code)
	}
	if rec := m.spend(audio.URL, nil); rec.Code != http.StatusOK {
		t.Fatalf("audio after rotation = %d", rec.Code)
	}
}

func TestMediaLocatorNestedLocatorsKeepAuthorityAndNeverExtendTheGrant(t *testing.T) {
	m := withMediaFixture(t)
	p := m.browser("Member", "1 hour")
	out, _ := m.mintVideo(p)
	parent := locatorFromURL(t, out.URL)

	master := m.spend(out.URL, nil)
	variantURL := lastLine(master.Body.String())
	variant := locatorFromURL(t, variantURL)
	if variant.Auth != parent.Auth || variant.User != parent.User || variant.Item != parent.Item {
		t.Fatalf("the nested locator changed its authority: %+v vs %+v", variant, parent)
	}
	if variant.Exp > parent.Exp {
		t.Fatalf("the nested locator outlives its parent: %d > %d", variant.Exp, parent.Exp)
	}
	media := m.spend(variantURL, nil)
	for _, segment := range nonCommentLines(media.Body.String()) {
		seg := locatorFromURL(t, segment)
		if seg.Auth != parent.Auth || seg.Exp > variant.Exp {
			t.Fatalf("a segment locator widened its grant: %+v (parent %+v)", seg, variant)
		}
	}

	// Rewriting later must not extend a grant: a locator close to its end yields
	// nested locators that end at the same instant, not twelve hours on.
	closing := variant
	closing.Exp = time.Now().Add(90 * time.Second).Unix()
	rec := m.spend("/api/v1/hls/"+signHLSLocator(closing), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("closing locator = %d", rec.Code)
	}
	for _, segment := range nonCommentLines(rec.Body.String()) {
		if seg := locatorFromURL(t, segment); seg.Exp > closing.Exp {
			t.Fatalf("rewriting extended a closing grant: %d > %d", seg.Exp, closing.Exp)
		}
	}
}

func TestMediaLocatorNeverLogsTheCapabilityOrTheUpstreamToken(t *testing.T) {
	m := withMediaFixture(t)
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	p := m.desktop("Member")
	video, _ := m.mintVideo(p)
	audio, _ := m.mintAudio(p)
	bodies := []string{}
	for _, path := range []string{video.URL, "/api/v1/hls/forged.token", audio.URL, "/api/v1/stream/audio-ticket/forged.token"} {
		bodies = append(bodies, m.spend(path, nil).Body.String())
	}
	_ = revokeDevice(context.Background(), p.deviceID)
	for _, path := range []string{video.URL, audio.URL} {
		bodies = append(bodies, m.spend(path, nil).Body.String())
	}
	tokenOf := func(u string) string { return u[strings.LastIndex(u, "/")+1:] }
	for _, secret := range []string{tokenOf(video.URL), tokenOf(audio.URL), capAdminToken, "SECRET-UPSTREAM"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log output contains %q", secret)
		}
		for _, body := range bodies {
			if strings.Contains(body, secret) {
				t.Errorf("an error body contains %q: %s", secret, body)
			}
		}
	}
}

func TestMediaLocatorInvalidCapabilitiesAreIndistinguishable(t *testing.T) {
	m := withMediaFixture(t)
	p := m.desktop("Member")
	out, _ := m.mintVideo(p)
	valid := locatorFromURL(t, out.URL)

	missingDevice := valid
	missingDevice.Auth.ID = "00000000-0000-4000-8000-00000000dead"
	notADevice := valid
	notADevice.Auth.ID = "not-a-uuid"
	wrongUser := valid
	wrongUser.User = "00000000-0000-4000-8000-00000000beef"
	bodies := map[string]string{}
	for name, loc := range map[string]hlsLocator{"missing device": missingDevice, "malformed device": notADevice, "wrong user": wrongUser} {
		rec := m.spend("/api/v1/hls/"+signHLSLocator(loc), nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		var parsed struct {
			Error struct{ Code, Message string }
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
		bodies[name] = parsed.Error.Code + "|" + parsed.Error.Message
	}
	forged := m.spend("/api/v1/hls/garbage", nil)
	var parsed struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(forged.Body.Bytes(), &parsed)
	bodies["forged"] = parsed.Error.Code + "|" + parsed.Error.Message
	for name, got := range bodies {
		if got != bodies["forged"] {
			t.Errorf("%s answers differently from a forged locator: %q vs %q", name, got, bodies["forged"])
		}
	}
}

func TestStreamTicketAudiobookSurvivesAccessRotationAndDiesWithTheDevice(t *testing.T) {
	m := withMediaFixture(t)
	p := m.desktop("Member")
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{ID: capVideoID, Surface: surface, Kind: "audiobook", Provider: ProviderGrimmory,
			UpstreamID: "book-1", Active: true, Available: true}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	rec := m.call(http.MethodPost, "/api/v1/library/audiobooks/"+capVideoID+"/stream-ticket", &p, `{"trackIndex":2}`)
	if rec.Code == http.StatusForbidden {
		// Member holds view_library in the shipped roles; a 403 here means the fixture lacks it.
		t.Fatalf("mint refused: %s", rec.Body.String())
	}
	var minted mintResult
	_ = json.Unmarshal(rec.Body.Bytes(), &minted)
	if rec.Code != http.StatusOK || !strings.HasPrefix(minted.URL, "/api/v1/library/audiobook-stream/") {
		t.Fatalf("audiobook mint = %d %s", rec.Code, rec.Body.String())
	}
	ticket, err := verifyStreamTicket(strings.TrimPrefix(minted.URL, "/api/v1/library/audiobook-stream/"), time.Now())
	if err != nil || ticket.Auth != (mediaAuthority{Kind: authorityDevice, ID: p.deviceID}) || ticket.Track != 2 {
		t.Fatalf("ticket = %+v err=%v", ticket, err)
	}
	if minted.ExpiresIn < 12*3600-5 {
		t.Fatalf("expiresIn = %d", minted.ExpiresIn)
	}

	// Past the ticket and authority checks the request needs Grimmory, which this
	// fixture lacks; the assertion is that it is not rejected as unauthorized.
	notUnauthorized := func(when string) {
		t.Helper()
		if rec := m.spend(minted.URL, nil); rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s: the ticket was rejected as unauthorized", when)
		}
	}
	notUnauthorized("before rotation")
	if _, _, err := rotateDeviceToken(context.Background(), p.refresh); err != nil {
		t.Fatal(err)
	}
	m.userDB.exec(`UPDATE sessions SET revoked_at = NOW() WHERE token_hash=$1`, sha256Hex(p.bearer))
	notUnauthorized("after the access token rotated")

	if err := revokeDevice(context.Background(), p.deviceID); err != nil {
		t.Fatal(err)
	}
	if rec := m.spend(minted.URL, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after revoking the device = %d, want 401", rec.Code)
	}
}

func TestStreamTicketAudioHonoursRange(t *testing.T) {
	m := withMediaFixture(t)
	out, _ := m.mintAudio(m.browser("Member", "1 hour"))
	rec := m.spend(out.URL, map[string]string{"Range": "bytes=100-149"})
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 50 || rec.Header().Get("Content-Range") != "bytes 100-149/200" {
		t.Fatalf("range = %d %q (%d bytes)", rec.Code, rec.Header().Get("Content-Range"), rec.Body.Len())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers: %v", rec.Header())
	}
	r := httptest.NewRequest(http.MethodHead, out.URL, nil)
	head := httptest.NewRecorder()
	m.handler.ServeHTTP(head, r)
	if head.Code != http.StatusOK {
		t.Fatalf("head = %d", head.Code)
	}
}
