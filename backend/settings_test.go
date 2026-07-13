package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCurrentTokenHash(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if h := currentTokenHash(r); h != "" {
		t.Errorf("no cookie should yield empty hash, got %q", h)
	}
	r.AddCookie(&http.Cookie{Name: "telos_session", Value: "abc"})
	want := sha256.Sum256([]byte("abc"))
	if h := currentTokenHash(r); h != hex.EncodeToString(want[:]) {
		t.Errorf("hash mismatch: %s", h)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — validation & guardrail helper tests
// ═══════════════════════════════════════════════════════════════════════════

func TestValidateDisplayName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"  Carter  ", "Carter", false},
		{"", "", false},
		{"   ", "", false},
		{strings.Repeat("a", 65), "", true},
		{"bad\x00name", "", true},
		{"ok name 123", "ok name 123", false},
	}
	for _, c := range cases {
		got, err := validateDisplayName(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("validateDisplayName(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Errorf("validateDisplayName(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateNewPassword(t *testing.T) {
	if err := validateNewPassword(strings.Repeat("x", 14)); err == nil {
		t.Error("14 chars should fail")
	}
	if err := validateNewPassword(strings.Repeat("x", 15)); err != nil {
		t.Errorf("15 chars should pass: %v", err)
	}
	if err := validateNewPassword(strings.Repeat("x", 129)); err == nil {
		t.Error("129 chars should fail")
	}
}

func TestValidatePreferences(t *testing.T) {
	good := []byte(`{"theme":"ink","sceneEnabled":false,"reducedMotion":true,
		"voiceInputDeviceId":"abc123","voiceOutputDeviceId":"",
		"voiceInputGain":1.5,"voiceOutputVolume":0.8,
		"voiceNoiseSuppression":true,"voiceEchoCancellation":false,
		"voiceAutoGainControl":true}`)
	prefs, err := validatePreferences(good)
	if err != nil {
		t.Fatalf("valid prefs rejected: %v", err)
	}
	if prefs["theme"] != "ink" {
		t.Errorf("theme = %v, want ink", prefs["theme"])
	}
	if prefs["voiceInputGain"] != 1.5 {
		t.Errorf("voiceInputGain = %v, want 1.5", prefs["voiceInputGain"])
	}
	bads := [][]byte{
		[]byte(`{"theme":"neon"}`),
		[]byte(`{"sceneEnabled":"yes"}`),
		[]byte(`{"evil":true}`),
		[]byte(`{`),
		[]byte(`{"theme":"` + strings.Repeat("a", 3000) + `"}`),
		[]byte(`{"voiceInputGain":3}`),
		[]byte(`{"voiceInputGain":-0.1}`),
		[]byte(`{"voiceInputGain":"loud"}`),
		[]byte(`{"voiceOutputVolume":1.1}`),
		[]byte(`{"voiceNoiseSuppression":"on"}`),
		[]byte(`{"voiceInputDeviceId":42}`),
		[]byte(`{"voiceInputDeviceId":"` + strings.Repeat("d", 300) + `"}`),
	}
	for i, b := range bads {
		if _, err := validatePreferences(b); err == nil {
			t.Errorf("case %d: invalid prefs accepted", i)
		}
	}
}

func TestSlugifyRoleID(t *testing.T) {
	got, err := slugifyRoleID("  Book Club Mods! ")
	if err != nil || got != "book-club-mods" {
		t.Errorf("got %q err %v", got, err)
	}
	if _, err := slugifyRoleID("!!!"); err == nil {
		t.Error("unsluggable name should fail")
	}
	if _, err := slugifyRoleID("a"); err == nil {
		t.Error("1-char slug should fail")
	}
}

func TestAvatarExtWhitelist(t *testing.T) {
	for ext, want := range map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
		".pdf": false, ".mp4": false, ".epub": false,
	} {
		if avatarExts[ext] != want {
			t.Errorf("avatarExts[%q] = %v, want %v", ext, avatarExts[ext], want)
		}
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() { dbPool = oldPool; pool.Close() }()

	var userID string
	err = dbPool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ('prefs-test-user', '!')
		ON CONFLICT (username) DO UPDATE SET username = EXCLUDED.username
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer dbPool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	if err := upsertPreferences(ctx, userID, map[string]any{"theme": "ink"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	prefs, err := loadPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if prefs["theme"] != "ink" {
		t.Errorf("theme = %v, want ink", prefs["theme"])
	}
}

func TestAnonymizeUserPreservesRow(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; requires live Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	oldPool := dbPool
	dbPool = pool
	defer func() { dbPool = oldPool; pool.Close() }()

	var userID string
	err = dbPool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ('anonymize-test-user', '!')
		RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer dbPool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	if err := anonymizeUser(ctx, userID); err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	var username string
	var active bool
	if err := dbPool.QueryRow(ctx,
		`SELECT username, active FROM users WHERE id = $1`, userID).Scan(&username, &active); err != nil {
		t.Fatalf("row gone after anonymize: %v", err)
	}
	if active || !strings.HasPrefix(username, "deleted-") {
		t.Errorf("got username=%q active=%v", username, active)
	}
}

func TestRoleChangeError(t *testing.T) {
	if err := roleChangeError("u1", true, "u1", false, []string{"Member"}); err == nil {
		t.Error("self role change should fail")
	}
	if err := roleChangeError("u1", false, "u2", false, []string{"Owner"}); err == nil {
		t.Error("non-owner granting Owner should fail")
	}
	if err := roleChangeError("u1", false, "u2", true, []string{"Member"}); err == nil {
		t.Error("non-owner revoking Owner should fail")
	}
	if err := roleChangeError("u1", true, "u2", false, []string{"Owner"}); err != nil {
		t.Errorf("owner granting Owner should pass: %v", err)
	}
	if err := roleChangeError("u1", false, "u2", false, []string{"Moderator"}); err != nil {
		t.Errorf("plain change should pass: %v", err)
	}
}
