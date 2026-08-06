package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type deviceDisconnector interface {
	DisconnectDevice(deviceID string) int
}

var hubInstance deviceDisconnector

type DeviceSummary struct {
	ID            string    `json:"id"`
	DeviceName    string    `json:"deviceName"`
	Platform      string    `json:"platform"`
	ClientVersion string    `json:"clientVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
}

type wsTicketEntry struct {
	userID    string
	deviceID  string
	expiresAt time.Time
}

type wsTicketStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]wsTicketEntry
}

var globalWSTicketStore = newWSTicketStore(30 * time.Second)

func newWSTicketStore(ttl time.Duration) *wsTicketStore {
	return &wsTicketStore{
		ttl:     ttl,
		tickets: make(map[string]wsTicketEntry),
	}
}

func (s *wsTicketStore) Issue(userID, deviceID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate ticket entropy: %w", err)
	}
	ticket := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[ticket] = wsTicketEntry{
		userID:    userID,
		deviceID:  deviceID,
		expiresAt: time.Now().Add(s.ttl),
	}
	return ticket, nil
}

func (s *wsTicketStore) Consume(ticket string) (userID, deviceID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tickets[ticket]
	if !ok {
		return "", "", errors.New("invalid or consumed ticket")
	}
	delete(s.tickets, ticket)

	if time.Now().After(entry.expiresAt) {
		return "", "", errors.New("expired ticket")
	}
	return entry.userID, entry.deviceID, nil
}

func generateSecureTokenPair() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("entropy failure: %w", err)
	}
	token = hex.EncodeToString(b)
	hash = sha256Hex(token)
	return token, hash, nil
}

func registerDevice(ctx context.Context, userID, deviceName, platform, clientVersion string) (deviceID, refreshToken, accessToken string, err error) {
	if dbPool == nil {
		return "", "", "", errors.New("db uninitialized")
	}
	if userID == "" || deviceName == "" || platform == "" || clientVersion == "" {
		return "", "", "", errors.New("missing required device registration parameters")
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO devices (user_id, device_name, platform, client_version, last_seen_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id
	`, userID, deviceName, platform, clientVersion).Scan(&deviceID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert device: %w", err)
	}

	rawRefresh, refreshHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", "", err
	}

	rawAccess, accessHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", "", err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
	`, deviceID, refreshHash)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert refresh token: %w", err)
	}

	// device_id ties this access token to the device so revocation can reach it.
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, client_ip, created_at, expires_at, device_id)
		VALUES ($1, $2, $3, '0.0.0.0', NOW(), NOW() + INTERVAL '15 minutes', $4)
	`, userID, accessHash, fmt.Sprintf("%s/%s (%s)", deviceName, clientVersion, platform), deviceID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to insert access token session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", err
	}

	return deviceID, rawRefresh, rawAccess, nil
}

func rotateDeviceToken(ctx context.Context, presentedRefresh string) (refreshToken, accessToken string, err error) {
	if dbPool == nil {
		return "", "", errors.New("db uninitialized")
	}
	presentedHash := sha256Hex(presentedRefresh)

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	var tokenID, deviceID, userID string
	var expiresAt time.Time
	var consumedAt *time.Time
	var replacedBy *string
	var revokedAt *time.Time

	err = tx.QueryRow(ctx, `
		SELECT rt.id, rt.device_id, d.user_id, rt.expires_at, rt.consumed_at, rt.replaced_by, d.revoked_at
		FROM device_refresh_tokens rt
		JOIN devices d ON d.id = rt.device_id
		WHERE rt.token_hash = $1
		FOR UPDATE OF rt
	`, presentedHash).Scan(&tokenID, &deviceID, &userID, &expiresAt, &consumedAt, &replacedBy, &revokedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errors.New("invalid refresh token")
		}
		return "", "", err
	}

	if revokedAt != nil {
		return "", "", errors.New("device revoked")
	}

	if consumedAt != nil || replacedBy != nil {
		// This credential was already rotated, so the copy being presented
		// leaked. Fail closed: revoke the whole device rather than just the
		// token. A legitimate client that lost a rotation response re-registers.
		if _, err := revokeDeviceTx(ctx, tx, deviceID); err != nil {
			return "", "", fmt.Errorf("reuse detected but revocation failed: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", "", fmt.Errorf("reuse detected but revocation commit failed: %w", err)
		}
		if hubInstance != nil {
			hubInstance.DisconnectDevice(deviceID)
		}
		return "", "", errors.New("refresh token reuse detected; device revoked")
	}

	if time.Now().After(expiresAt) {
		return "", "", errors.New("refresh token expired")
	}

	newRawRefresh, newRefreshHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", err
	}

	newRawAccess, newAccessHash, err := generateSecureTokenPair()
	if err != nil {
		return "", "", err
	}

	var successorID string
	err = tx.QueryRow(ctx, `
		INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '90 days')
		RETURNING id
	`, deviceID, newRefreshHash).Scan(&successorID)
	if err != nil {
		return "", "", err
	}

	_, err = tx.Exec(ctx, `
		UPDATE device_refresh_tokens
		SET consumed_at = NOW(), replaced_by = $1
		WHERE id = $2
	`, successorID, tokenID)
	if err != nil {
		return "", "", err
	}

	_, _ = tx.Exec(ctx, `UPDATE devices SET last_seen_at = NOW() WHERE id = $1`, deviceID)

	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, client_ip, created_at, expires_at, device_id)
		VALUES ($1, $2, 'TelosDevice/1.0', '0.0.0.0', NOW(), NOW() + INTERVAL '15 minutes', $3)
	`, userID, newAccessHash, deviceID)
	if err != nil {
		return "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}

	return newRawRefresh, newRawAccess, nil
}

func issueWSTicket(ctx context.Context, userID, deviceID string) (ticket string, err error) {
	if userID == "" {
		return "", errors.New("unauthorized: missing user identity")
	}
	return globalWSTicketStore.Issue(userID, deviceID)
}

func consumeWSTicket(ctx context.Context, ticket string) (userID, deviceID string, err error) {
	return globalWSTicketStore.Consume(ticket)
}

// revokeDeviceTx marks a device revoked and invalidates every access token
// issued to it, in one transaction. Both halves are required: LoadAuthenticatedUser
// only filters on sessions.revoked_at, so flagging the device alone would leave a
// working bearer token in the caller's hands until it expired. Returns the number
// of device rows affected, which is zero if it was already revoked.
func revokeDeviceTx(ctx context.Context, tx pgx.Tx, deviceID string) (int64, error) {
	res, err := tx.Exec(ctx, `
		UPDATE devices SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL
	`, deviceID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = NOW() WHERE device_id = $1 AND revoked_at IS NULL
	`, deviceID); err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

func revokeDevice(ctx context.Context, deviceID string) error {
	if dbPool == nil {
		return errors.New("db uninitialized")
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	affected, err := revokeDeviceTx(ctx, tx, deviceID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("device not found or already revoked")
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if hubInstance != nil {
		hubInstance.DisconnectDevice(deviceID)
	}
	return nil
}

func listDevices(ctx context.Context, userID string) ([]DeviceSummary, error) {
	if dbPool == nil {
		return nil, errors.New("db uninitialized")
	}
	rows, err := dbPool.Query(ctx, `
		SELECT id, device_name, platform, client_version, created_at, last_seen_at
		FROM devices
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DeviceSummary
	for rows.Next() {
		var d DeviceSummary
		if err := rows.Scan(&d.ID, &d.DeviceName, &d.Platform, &d.ClientVersion, &d.CreatedAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if result == nil {
		result = []DeviceSummary{}
	}
	return result, nil
}
