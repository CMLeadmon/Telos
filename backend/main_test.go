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
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

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


func TestHandleHealth(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHealth)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusServiceUnavailable && status != http.StatusOK {
		t.Errorf("handler returned unexpected status code: got %v", status)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	if resp.Status == "" {
		t.Errorf("expected health status to be populated")
	}
}

// Requires a live Postgres with the Telos schema applied; skipped otherwise.
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

	channel := "tdd-history-test"
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM messages WHERE channel_id = $1`, channel)
		pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channel)
	}
	cleanup()
	defer cleanup()

	if _, err := pool.Exec(ctx, `INSERT INTO channels (id, name, type) VALUES ($1, $1, 'text')`, channel); err != nil {
		t.Fatalf("Failed to insert test channel: %v", err)
	}
	for i := 1; i <= 60; i++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO messages (channel_id, user_id, content, timestamp)
			VALUES ($1, 'telos-bot', $2, now() - make_interval(secs => $3))
		`, channel, fmt.Sprintf("msg-%d", i), 60-i)
		if err != nil {
			t.Fatalf("Failed to insert test message %d: %v", i, err)
		}
	}

	msgs, err := getMessagesForChannel(ctx, channel)
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

func TestVoiceTokenRejectsMissingUser(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")

	req := httptest.NewRequest("GET", "/api/v1/voice/token?room=test-room", nil)
	rr := httptest.NewRecorder()
	handleLiveKitToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing user param, got %d", rr.Code)
	}
}

func TestVoiceTokenRejectsUnconfiguredCredentials(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "")
	t.Setenv("LIVEKIT_API_SECRET", "")

	req := httptest.NewRequest("GET", "/api/v1/voice/token?room=test-room&user=alice", nil)
	rr := httptest.NewRecorder()
	handleLiveKitToken(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500 for unconfigured LiveKit credentials, got %d", rr.Code)
	}
}

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

// Exercises the WS handler end-to-end in loopback mode (no DB, no Redis).
func TestWebSocketLoopbackBroadcast(t *testing.T) {
	oldPool, oldRedis := dbPool, redisClient
	dbPool, redisClient = nil, nil
	defer func() { dbPool, redisClient = oldPool, oldRedis }()

	srv := httptest.NewServer(http.HandlerFunc(handleWebSocket))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?channel=test&user=alice"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial WebSocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]string{"content": "hello loopback"}); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	var notif WSNotification
	if err := conn.ReadJSON(&notif); err != nil {
		t.Fatalf("Failed to read broadcast: %v", err)
	}
	if notif.Type != "message" || notif.Message == nil {
		t.Fatalf("Expected a message notification, got %+v", notif)
	}
	if notif.Message.Content != "hello loopback" {
		t.Errorf("Expected echoed content, got %q", notif.Message.Content)
	}
	if notif.Message.Sender != "alice" {
		t.Errorf("Expected sender alice, got %q", notif.Message.Sender)
	}
}


