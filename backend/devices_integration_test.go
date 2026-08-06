package main

import (
	"context"
	"testing"
	"time"
)

func TestRegisterDeviceAndRotateToken(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('devuser','x') RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Register device
	deviceID, refresh1, access1, err := registerDevice(ctx, userID, "Desktop App", "linux", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice failed: %v", err)
	}
	if deviceID == "" || refresh1 == "" || access1 == "" {
		t.Fatalf("invalid tokens returned from registerDevice")
	}

	// Verify device in DB
	devs, err := listDevices(ctx, userID)
	if err != nil || len(devs) != 1 {
		t.Fatalf("listDevices returned %d devices, err=%v", len(devs), err)
	}
	if devs[0].ID != deviceID || devs[0].DeviceName != "Desktop App" {
		t.Fatalf("device summary mismatch: %+v", devs[0])
	}

	// Rotate refresh token
	refresh2, access2, err := rotateDeviceToken(ctx, refresh1)
	if err != nil {
		t.Fatalf("rotateDeviceToken failed: %v", err)
	}
	if refresh2 == refresh1 || access2 == access1 {
		t.Fatalf("token rotation did not generate new tokens")
	}

	// Replay Detection: presenting refresh1 again must fail and revoke the device
	_, _, err = rotateDeviceToken(ctx, refresh1)
	if err == nil {
		t.Fatalf("negative test failed: replayed refresh token was accepted")
	}

	// Verify device is revoked after replay attack
	devsAfter, err := listDevices(ctx, userID)
	if err != nil || len(devsAfter) != 0 {
		t.Fatalf("expected 0 active devices after replay detection revocation, got %d", len(devsAfter))
	}
}

// A revoked device must lose its access token immediately, not merely stop
// being able to refresh. LoadAuthenticatedUser filters on sessions.revoked_at,
// so this is the check that proves revocation actually reaches the bearer
// credential rather than only flagging the device row.
func TestRevokeDeviceInvalidatesAccessTokenImmediately(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('revokeuser','x') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	deviceID, _, access, err := registerDevice(ctx, userID, "Phone", "ios", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice: %v", err)
	}

	if _, err := LoadAuthenticatedUser(ctx, f.DB, sha256Hex(access)); err != nil {
		t.Fatalf("access token should authenticate before revocation: %v", err)
	}

	if err := revokeDevice(ctx, deviceID); err != nil {
		t.Fatalf("revokeDevice: %v", err)
	}

	if _, err := LoadAuthenticatedUser(ctx, f.DB, sha256Hex(access)); err == nil {
		t.Fatal("access token still authenticates after its device was revoked")
	}
}

// Replaying an already-rotated refresh token means the credential leaked. The
// device must be revoked and its outstanding access tokens killed with it.
func TestRefreshReplayRevokesDeviceAndItsAccessTokens(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('replayuser','x') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	deviceID, refresh1, _, err := registerDevice(ctx, userID, "Laptop", "linux", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice: %v", err)
	}

	_, access2, err := rotateDeviceToken(ctx, refresh1)
	if err != nil {
		t.Fatalf("first rotation should succeed: %v", err)
	}
	if _, err := LoadAuthenticatedUser(ctx, f.DB, sha256Hex(access2)); err != nil {
		t.Fatalf("rotated access token should authenticate: %v", err)
	}

	// Replay the consumed token.
	if _, _, err := rotateDeviceToken(ctx, refresh1); err == nil {
		t.Fatal("replaying a consumed refresh token should fail")
	}

	var revokedAt *time.Time
	if err := f.DB.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceID).Scan(&revokedAt); err != nil {
		t.Fatalf("read device: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("replay did not revoke the device")
	}

	if _, err := LoadAuthenticatedUser(ctx, f.DB, sha256Hex(access2)); err == nil {
		t.Fatal("access token still authenticates after replay-triggered revocation")
	}
}
