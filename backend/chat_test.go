package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func seedChannel(t *testing.T, name string) string {
	t.Helper()
	var id string
	err := dbPool.QueryRow(context.Background(),
		`INSERT INTO channels (name, type) VALUES ($1,'text') RETURNING id::text`, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

func TestAuthorizeChannelMatrix(t *testing.T) {
	withFixture(t)
	ctx := context.Background()
	member := &UserContext{ID: "u1", Roles: []string{"Member"}}
	noPerms := &UserContext{ID: "u2", Roles: []string{}}

	chID := seedChannel(t, "general")

	cases := []struct {
		name    string
		user    *UserContext
		channel string
		action  ChannelAction
		wantErr error
	}{
		{"malformed uuid", member, "not-a-uuid", ChannelView, errChannelNotFound},
		{"nonexistent channel", member, "00000000-0000-0000-0000-000000000009", ChannelView, errChannelNotFound},
		{"member can view", member, chID, ChannelView, nil},
		{"member can send", member, chID, ChannelSend, nil},
		{"member can voice", member, chID, ChannelVoice, nil},
		{"no-perms view denied as not-found", noPerms, chID, ChannelView, errChannelNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AuthorizeChannel(ctx, tc.user, tc.channel, tc.action)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAuthorizeChannelViewDenyOverride(t *testing.T) {
	withFixture(t)
	ctx := context.Background()
	chID := seedChannel(t, "restricted")
	// Explicitly deny view_channel for Member on this channel.
	if _, err := dbPool.Exec(ctx, `
		INSERT INTO channel_permission_overrides (channel_id, role_id, permission_id, decision)
		VALUES ($1, 'Member', 'view_channel', 'deny')
	`, chID); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	member := &UserContext{ID: "u1", Roles: []string{"Member"}}
	if err := AuthorizeChannel(ctx, member, chID, ChannelView); !errors.Is(err, errChannelNotFound) {
		t.Fatalf("view-denied channel err = %v, want errChannelNotFound", err)
	}
}

// TestWebSocketRevocationClosesLiveSocket proves S02: revoking a user's access
// closes the live socket rather than leaving it connected.
func TestWebSocketRevocationClosesLiveSocket(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	// Seed a Member user, a live session, and a channel.
	var userID string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('member','x') RETURNING id`).Scan(&userID)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Member')`, userID)
	chID := seedChannel(t, "lounge")
	token := "live-session-token-abcdef0123456789"
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2, NOW() + INTERVAL '1 hour')`,
		sha256Hex(token), userID)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/chat/ws", withAuth(http.HandlerFunc(handleWebSocket), "view_channel"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Allow the test origin through the WS upgrade origin check.
	origin := srv.URL
	oldCfg := securityConfig
	securityConfig.Environment = "development"
	securityConfig.AllowedDevOrigins = map[string]struct{}{}
	if norm, err := normalizeOrigin(origin); err == nil {
		securityConfig.AllowedDevOrigins[norm.String()] = struct{}{}
	}
	t.Cleanup(func() { securityConfig = oldCfg })

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/chat/ws?channel=" + chID
	header := http.Header{}
	header.Set("Cookie", "telos_session="+token)
	header.Set("Origin", origin)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Drain the history frame so the socket is fully established.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, _ = conn.ReadMessage()

	// Revoke and assert the next read fails (socket closed by the server).
	revokeUserSockets(userID)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	closed := false
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("socket was not closed after revocation")
	}
	// Wait for the server handler to fully release before the fixture cleanup
	// restores the package globals (otherwise the still-running handler races
	// the restore).
	deadline := time.Now().Add(3 * time.Second)
	for sessionRegistryInstance.LiveCount(userID) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := sessionRegistryInstance.LiveCount(userID); n != 0 {
		t.Fatalf("handler did not release socket: live count %d", n)
	}
}
