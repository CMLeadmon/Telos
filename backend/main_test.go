package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ═══════════════════════════════════════════════════════════════════════════
// Argon2id Password Hashing Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestPasswordHashing(t *testing.T) {
	password := "super-secure-password-12345"

	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("Expected Argon2id format prefix, got %s", hash)
	}

	ok, err := verifyPassword(password, hash)
	if err != nil {
		t.Fatalf("Failed to verify password: %v", err)
	}
	if !ok {
		t.Errorf("Password verification failed for correct password")
	}

	ok, err = verifyPassword("wrong-password", hash)
	if err != nil {
		t.Fatalf("Failed to verify password: %v", err)
	}
	if ok {
		t.Errorf("Password verification succeeded for incorrect password")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// LiveKit Token Generation Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestGenerateLiveKitToken(t *testing.T) {
	apiKey := "test-key"
	apiSecret := "test-secret-at-least-thirty-two-chars"
	roomName := "test-room"
	identity := "test-user"

	token, err := GenerateLiveKitToken(apiKey, apiSecret, roomName, identity)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("Token does not have 3 parts: %s", token)
	}

	// 1. Verify Header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("Failed to decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatalf("Failed to unmarshal header: %v", err)
	}
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Errorf("Unexpected header values: %+v", header)
	}

	// 2. Verify Claims
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("Failed to decode claims: %v", err)
	}
	var claims LiveKitClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatalf("Failed to unmarshal claims: %v", err)
	}

	if claims.Iss != apiKey {
		t.Errorf("Expected issuer %s, got %s", apiKey, claims.Iss)
	}
	if claims.Sub != identity {
		t.Errorf("Expected subject %s, got %s", identity, claims.Sub)
	}
	if !claims.Video.RoomJoin || claims.Video.Room != roomName {
		t.Errorf("Unexpected video grants: %+v", claims.Video)
	}

	// 3. Verify Signature
	signingInput := parts[0] + "." + parts[1]
	key := []byte(apiSecret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(signingInput))
	expectedSignature := h.Sum(nil)

	actualSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("Failed to decode signature: %v", err)
	}

	if !hmac.Equal(expectedSignature, actualSignature) {
		t.Errorf("Signature verification failed")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Health Endpoint Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestHandleHealth(t *testing.T) {
	oldPool, oldRedis := dbPool, redisClient
	dbPool, redisClient = nil, nil
	defer func() {
		dbPool = oldPool
		redisClient = oldRedis
	}()

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rr := httptest.NewRecorder()

	handleHealth(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable when DB and Redis are nil, got %d", rr.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	if resp.Status != "unhealthy" {
		t.Errorf("Expected overall status to be unhealthy, got %q", resp.Status)
	}
	if resp.Services["database"] != "uninitialized" {
		t.Errorf("Expected database service to be uninitialized, got %q", resp.Services["database"])
	}
	if resp.Services["redis"] != "uninitialized" {
		t.Errorf("Expected redis service to be uninitialized, got %q", resp.Services["redis"])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Database Query Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestGetMessagesForChannelReturnsLatest(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() {
		dbPool = oldPool
		pool.Close()
	}()

	channelID := "00000000-0000-0000-0000-000000000099"
	userID := "00000000-0000-0000-0000-000000000088"

	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM messages WHERE channel_id = $1`, channelID)
		pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	}
	cleanup()
	defer cleanup()

	if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, 'test-bot-user', 'hash')`, userID); err != nil {
		t.Fatalf("Failed to insert test user: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO channels (id, name, type) VALUES ($1, 'tdd-history-test', 'text')`, channelID); err != nil {
		t.Fatalf("Failed to insert test channel: %v", err)
	}

	for i := 1; i <= 60; i++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO messages (channel_id, user_id, content, created_at)
			VALUES ($1, $2, $3, now() - make_interval(secs => $4))
		`, channelID, userID, fmt.Sprintf("msg-%d", i), 60-i)
		if err != nil {
			t.Fatalf("Failed to insert test message %d: %v", i, err)
		}
	}

	msgs, err := getMessagesForChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("getMessagesForChannel failed: %v", err)
	}

	if len(msgs) != 50 {
		t.Fatalf("Expected 50 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "msg-11" {
		t.Errorf("Expected oldest returned message to be msg-11, got %s", msgs[0].Content)
	}
	if msgs[49].Content != "msg-60" {
		t.Errorf("Expected newest returned message to be msg-60 (the latest), got %s", msgs[49].Content)
	}
}

func TestHandleListChannelsIncludesSeeds(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() {
		dbPool = oldPool
		pool.Close()
	}()

	req := httptest.NewRequest("GET", "/api/v1/channels", nil)
	rec := httptest.NewRecorder()
	handleListChannels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}

	var channels []ChannelResponse
	if err := json.NewDecoder(rec.Body).Decode(&channels); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	byName := map[string]ChannelResponse{}
	for _, c := range channels {
		byName[c.Name] = c
	}
	general, ok := byName["general"]
	if !ok {
		t.Fatal("Expected seeded channel 'general' in response")
	}
	if general.Type != "text" {
		t.Errorf("Expected 'general' to be a text channel, got %q", general.Type)
	}
	if lounge, ok := byName["voice-lounge"]; ok && lounge.Type != "voice" {
		t.Errorf("Expected 'voice-lounge' to be a voice channel, got %q", lounge.Type)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Jellyfin API Client Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestGetJellyfinUserIDPrefersConfiguredUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]string{
			{"Id": "id-alice", "Name": "alice"},
			{"Id": "id-bob", "Name": "bob"},
		})
	}))
	defer srv.Close()

	oldBase := jellyfinBaseURL
	jellyfinBaseURL = srv.URL
	defer func() { jellyfinBaseURL = oldBase }()

	oldRedis := redisClient
	redisClient = nil
	defer func() { redisClient = oldRedis }()

	t.Setenv("JELLYFIN_USER_NAME", "bob")
	id, err := getJellyfinUserID(context.Background())
	if err != nil {
		t.Fatalf("getJellyfinUserID failed: %v", err)
	}
	if id != "id-bob" {
		t.Errorf("Expected configured user id-bob, got %s", id)
	}

	t.Setenv("JELLYFIN_USER_NAME", "")
	id, err = getJellyfinUserID(context.Background())
	if err != nil {
		t.Fatalf("getJellyfinUserID failed: %v", err)
	}
	if id != "id-alice" {
		t.Errorf("Expected fallback to first user id-alice, got %s", id)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Jellyfin Item Hierarchy Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestMapJellyfinChildren(t *testing.T) {
	cases := []struct {
		name  string
		input jellyfinChildItem
		want  MediaPlayableItem
	}{
		{
			name: "folder with children has no duration",
			input: jellyfinChildItem{
				ID: "series-1", Name: "King of the Hill", Type: "Series",
				IsFolder: true, ChildCount: 13,
			},
			want: MediaPlayableItem{
				ID: "series-1", Title: "King of the Hill", Duration: "0m",
				Type: "Series", IsFolder: true, ChildCount: 13,
			},
		},
		{
			name: "leaf under an hour shows minutes only",
			input: jellyfinChildItem{
				ID: "ep-1", Name: "Pilot", Type: "Episode",
				RunTimeTicks: 13_000_000_000, IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "ep-1", Title: "Pilot", Duration: "21m",
				Type: "Episode", IsFolder: false,
			},
		},
		{
			name: "leaf over an hour shows hours and minutes",
			input: jellyfinChildItem{
				ID: "movie-1", Name: "Office Space", Type: "Movie",
				RunTimeTicks: 54_000_000_000, IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "movie-1", Title: "Office Space", Duration: "1h 30m",
				Type: "Movie", IsFolder: false,
			},
		},
		{
			name: "zero runtime leaf falls back to 0m",
			input: jellyfinChildItem{
				ID: "track-1", Name: "Chapter 1", Type: "Audio", IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "track-1", Title: "Chapter 1", Duration: "0m",
				Type: "Audio", IsFolder: false,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapJellyfinChildren([]jellyfinChildItem{tc.input})
			if len(got) != 1 {
				t.Fatalf("mapJellyfinChildren returned %d items, want 1", len(got))
			}
			if got[0] != tc.want {
				t.Errorf("mapJellyfinChildren(%+v) = %+v, want %+v", tc.input, got[0], tc.want)
			}
		})
	}
}

func TestHandleMediaItemsReturnsDirectChildren(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Users":
			json.NewEncoder(w).Encode([]map[string]string{{"Id": "user-1", "Name": "admin"}})
		case r.URL.Path == "/Users/user-1/Items":
			if got := r.URL.Query().Get("ParentId"); got != "series-1" {
				t.Errorf("ParentId = %q, want series-1", got)
			}
			if got := r.URL.Query().Get("Recursive"); got != "" {
				t.Errorf("Recursive = %q, want unset — recursion must stay off so only direct children come back", got)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"Items": []map[string]any{
					{"Id": "season-1", "Name": "Season 1", "Type": "Season", "IsFolder": true, "ChildCount": 13},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldBase := jellyfinBaseURL
	jellyfinBaseURL = srv.URL
	defer func() { jellyfinBaseURL = oldBase }()

	oldRedis := redisClient
	redisClient = nil
	defer func() { redisClient = oldRedis }()

	req := httptest.NewRequest("GET", "/api/v1/media/items?parentId=series-1", nil)
	rr := httptest.NewRecorder()
	handleMediaItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got []MediaPlayableItem
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	if !got[0].IsFolder || got[0].ChildCount != 13 || got[0].Title != "Season 1" {
		t.Errorf("got %+v, want a Season 1 folder with ChildCount 13", got[0])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// WebSocket Origin Policy Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestIsAllowedCSRFOrigin(t *testing.T) {
	cases := []struct {
		name             string
		rawURL           string
		requestHost      string
		configuredDomain string
		envMode          string
		want             bool
	}{
		{"same origin exact", "http://100.64.1.5:8080", "100.64.1.5:8080", "telos.local", "", true},
		{"tailscale magicdns same host", "https://node.tailnet.ts.net", "node.tailnet.ts.net", "telos.local", "", true},
		{"configured domain", "https://telos.local", "10.0.0.2:8080", "telos.local", "", true},
		{"foreign origin rejected", "https://evil.example", "100.64.1.5:8080", "telos.local", "", false},
		{"dev localhost cross-port", "http://localhost:3000", "localhost:8080", "telos.local", "development", true},
		{"dev same hostname cross-port", "http://100.64.1.5:3000", "100.64.1.5:8080", "telos.local", "development", true},
		{"prod same hostname cross-port rejected", "http://100.64.1.5:3000", "100.64.1.5:8080", "telos.local", "", false},
		{"empty origin rejected", "", "localhost:8080", "telos.local", "development", false},
		{"garbage origin rejected", "::not-a-url::", "localhost:8080", "telos.local", "development", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isAllowedCSRFOrigin(tc.rawURL, tc.requestHost, tc.configuredDomain, tc.envMode)
			if got != tc.want {
				t.Errorf("isAllowedCSRFOrigin(%q, %q, %q, %q) = %v, want %v",
					tc.rawURL, tc.requestHost, tc.configuredDomain, tc.envMode, got, tc.want)
			}
		})
	}
}

func TestIsAllowedWSOrigin(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		host    string
		domain  string
		allowed bool
	}{
		{"no origin (non-browser client)", "", "telos.local", "telos.local", true},
		{"same origin", "https://telos.local", "telos.local", "telos.local", true},
		{"configured domain", "https://telos.local", "10.0.0.5:8080", "telos.local", true},
		{"localhost dev", "http://localhost:3000", "localhost:8080", "telos.local", true},
		{"loopback dev", "http://127.0.0.1:3000", "127.0.0.1:8080", "telos.local", true},
		{"foreign origin", "https://evil.example.com", "telos.local", "telos.local", false},
		{"malformed origin", "::not-a-url::", "telos.local", "telos.local", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAllowedWSOrigin(c.origin, c.host, c.domain); got != c.allowed {
				t.Errorf("isAllowedWSOrigin(%q, %q, %q) = %v, want %v", c.origin, c.host, c.domain, got, c.allowed)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Reverse-Proxy CORS Header Tests
// ═══════════════════════════════════════════════════════════════════════════

// The gateway's corsMiddleware is the single CORS authority. Upstream services
// (Jellyfin) send their own Access-Control-Allow-Origin, and ReverseProxy
// appends response headers — without stripping, browsers see the illegal
// duplicate "http://localhost:3000, *" and block every proxied stream
// response in the cross-origin dev split (:3000 → :8080).
func TestProxyRequestStripsUpstreamCORSHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U")
	}))
	defer upstream.Close()

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequest(w, r, upstream.URL+"/Videos/x/main.m3u8", "test-token")
	}))

	req := httptest.NewRequest("GET", "/api/v1/stream/video/x/main.m3u8", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	acao := rr.Result().Header.Values("Access-Control-Allow-Origin")
	if len(acao) != 1 {
		t.Fatalf("Access-Control-Allow-Origin has %d values %v, want exactly 1", len(acao), acao)
	}
	if acao[0] != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the echoed request origin", acao[0])
	}
	if creds := rr.Result().Header.Values("Access-Control-Allow-Credentials"); len(creds) != 1 {
		t.Errorf("Access-Control-Allow-Credentials has %d values %v, want exactly 1", len(creds), creds)
	}
	if rr.Body.String() != "#EXTM3U" {
		t.Errorf("proxied body = %q, want upstream body", rr.Body.String())
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Media path safety
// ═══════════════════════════════════════════════════════════════════════════

func TestResolveMediaPath(t *testing.T) {
	old := mediaRoot
	mediaRoot = "/data/shared/media"
	defer func() { mediaRoot = old }()

	cases := []struct {
		name    string
		rel     string
		want    string
		wantErr bool
	}{
		{"root", "", "/data/shared/media", false},
		{"child", "vacation", "/data/shared/media/vacation", false},
		{"nested", "vacation/2024", "/data/shared/media/vacation/2024", false},
		{"dirty slashes", "a//b/", "/data/shared/media/a/b", false},
		{"leading slash stays inside", "/etc/passwd", "/data/shared/media/etc/passwd", false},
		{"escape dotdot", "../etc", "", true},
		{"escape nested dotdot", "a/../../x", "", true},
		{"nul byte", "a\x00b", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMediaPath(c.rel)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveMediaPath(%q) = %q, want error", c.rel, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMediaPath(%q) unexpected error: %v", c.rel, err)
			}
			if got != c.want {
				t.Errorf("resolveMediaPath(%q) = %q, want %q", c.rel, got, c.want)
			}
		})
	}
}

func TestWSEventDeleteMarshal(t *testing.T) {
	b, err := json.Marshal(WSEvent{Type: "message.delete", MessageID: "abc", ChannelID: "c1"})
	if err != nil {
		t.Fatalf("failed to marshal WSEvent: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"type":"message.delete"`) || !strings.Contains(got, `"messageId":"abc"`) {
		t.Fatalf("unexpected: %s", got)
	}
	if strings.Contains(got, `"reaction"`) || strings.Contains(got, `"messages"`) {
		t.Fatalf("omitempty leaked empty fields: %s", got)
	}
}

func TestIsEmoji(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"🔥", true},
		{"👍", true},
		{"✅", true},
		{"abc", false},
		{"123", false},
		{"", false},
		{"this-is-a-very-long-string-that-is-not-an-emoji", false},
	}
	for _, c := range cases {
		if got := isEmoji(c.input); got != c.want {
			t.Errorf("isEmoji(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}


// ═══════════════════════════════════════════════════════════════════════════
// Per-directory file listing
// ═══════════════════════════════════════════════════════════════════════════

func TestHandleListFilesReturnsFoldersAndFiles(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "vacation"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "root.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "vacation", "beach.jpg"), []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}

	// Root listing: one folder (vacation), one file (root.txt); beach.jpg is NOT here.
	req := httptest.NewRequest("GET", "/api/v1/files?path=&page=1", nil)
	rr := httptest.NewRecorder()
	handleListFiles(rr, req)
	if rr.Code != 200 {
		t.Fatalf("root list status = %d, want 200", rr.Code)
	}
	var root struct {
		Path    string `json:"path"`
		Folders []struct {
			ID, Name, Path string
		} `json:"folders"`
		Files []struct {
			ID, Filename string
		} `json:"files"`
		HasNext bool `json:"hasNext"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &root); err != nil {
		t.Fatalf("unmarshal root: %v (body %s)", err, rr.Body.String())
	}
	if len(root.Folders) != 1 || root.Folders[0].Name != "vacation" {
		t.Fatalf("root folders = %+v, want one 'vacation'", root.Folders)
	}
	if len(root.Files) != 1 || root.Files[0].Filename != "root.txt" {
		t.Fatalf("root files = %+v, want one 'root.txt'", root.Files)
	}

	// Enter vacation: beach.jpg present, as a basename.
	req2 := httptest.NewRequest("GET", "/api/v1/files?path=vacation&page=1", nil)
	rr2 := httptest.NewRecorder()
	handleListFiles(rr2, req2)
	var sub struct {
		Files []struct{ Filename string } `json:"files"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &sub); err != nil {
		t.Fatalf("unmarshal sub: %v", err)
	}
	if len(sub.Files) != 1 || sub.Files[0].Filename != "beach.jpg" {
		t.Fatalf("vacation files = %+v, want one 'beach.jpg'", sub.Files)
	}

	// Bad path → 400.
	reqBad := httptest.NewRequest("GET", "/api/v1/files?path=../etc&page=1", nil)
	rrBad := httptest.NewRecorder()
	handleListFiles(rrBad, reqBad)
	if rrBad.Code != 400 {
		t.Fatalf("escaping path status = %d, want 400", rrBad.Code)
	}
}



