//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// QuotaPolicy bounds per-user physical usage and the node free-space reserve.
type QuotaPolicy struct {
	PerUserPhysicalBytes int64
	NodeReserveBytes     int64
	ReservationTTL       time.Duration
}

// DefaultQuotaPolicy: 1 GiB/user, 5 GiB node reserve, 15-minute reservations.
func DefaultQuotaPolicy() QuotaPolicy {
	return QuotaPolicy{
		PerUserPhysicalBytes: 1 << 30,
		NodeReserveBytes:     5 << 30,
		ReservationTTL:       15 * time.Minute,
	}
}

func (p QuotaPolicy) validate() error {
	if p.PerUserPhysicalBytes <= 0 || p.NodeReserveBytes <= 0 || p.ReservationTTL <= 0 {
		return errors.New("quota policy values must be positive")
	}
	return nil
}

// quotaAdvisoryLock serializes the check-and-reserve so concurrent uploads
// cannot overbook capacity.
const quotaAdvisoryLock int64 = 0x54_4C_4F_53_51 // "TLOSQ"

var (
	errUserQuotaExceeded = errors.New("quota_user_exceeded")
	errNodeSpaceLow      = errors.New("quota_node_space_low")
	errQuotaOverflow     = errors.New("quota_size_invalid")
)

// QuotaReservation is a held reservation.
type QuotaReservation struct {
	ID     string
	guard  *QuotaGuard
	userID string
}

// QuotaGuard enforces quotas against the catalog and the filesystem.
type QuotaGuard struct {
	policy    QuotaPolicy
	storageFS string // a path on the storage filesystem, for statfs
}

func newQuotaGuard(policy QuotaPolicy, storageFS string) *QuotaGuard {
	return &QuotaGuard{policy: policy, storageFS: storageFS}
}

// freeBytes returns the current filesystem free space via statfs.
func (g *QuotaGuard) freeBytes() (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(g.storageFS, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Reserve charges finalBytes to the user and peakTemporaryBytes to the node,
// checking the per-user physical total (all charged lifecycle states plus live
// reservations) and the node free-space reserve under an advisory lock.
func (g *QuotaGuard) Reserve(ctx context.Context, userID string, finalBytes, peakTemporaryBytes int64) (*QuotaReservation, error) {
	if finalBytes < 0 || peakTemporaryBytes < 0 || finalBytes > (1<<62) || peakTemporaryBytes > (1<<62) {
		return nil, errQuotaOverflow
	}
	if err := g.policy.validate(); err != nil {
		return nil, err
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", quotaAdvisoryLock); err != nil {
		return nil, err
	}

	// Per-user charged physical bytes: every user-owned non-deleted lifecycle
	// asset plus that user's live reservations.
	var userCharged int64
	if err := tx.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT SUM(size_bytes) FROM files
		            WHERE uploader_id = $1 AND state <> 'deleted' AND state <> 'consumed'), 0)
		+ COALESCE((SELECT SUM(bytes) FROM upload_reservations
		            WHERE user_id = $1 AND expires_at > NOW()), 0)
	`, userID).Scan(&userCharged); err != nil {
		return nil, err
	}
	if userCharged+finalBytes > g.policy.PerUserPhysicalBytes {
		return nil, errUserQuotaExceeded
	}

	// Node admission: statfs free minus all live reservations minus unowned
	// orphan/quarantine bytes, must stay above the reserve after this peak.
	free, err := g.freeBytes()
	if err != nil {
		return nil, err
	}
	var liveReservations, orphanBytes int64
	tx.QueryRow(ctx, `SELECT COALESCE(SUM(bytes),0) FROM upload_reservations WHERE expires_at > NOW()`).Scan(&liveReservations)
	tx.QueryRow(ctx, `SELECT COALESCE(SUM(size_bytes),0) FROM files WHERE uploader_id IS NULL OR state = 'quarantined'`).Scan(&orphanBytes)
	adjustedFree := free - liveReservations - orphanBytes
	if adjustedFree-peakTemporaryBytes < g.policy.NodeReserveBytes {
		return nil, errNodeSpaceLow
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO upload_reservations (user_id, bytes, expires_at)
		VALUES ($1, $2, NOW() + $3::interval) RETURNING id::text
	`, userID, peakTemporaryBytes, fmt.Sprintf("%d seconds", int(g.policy.ReservationTTL.Seconds()))).Scan(&id); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &QuotaReservation{ID: id, guard: g, userID: userID}, nil
}

// Release removes a reservation (idempotent).
func (r *QuotaReservation) Release(ctx context.Context) error {
	if r == nil {
		return nil
	}
	_, err := dbPool.Exec(ctx, `DELETE FROM upload_reservations WHERE id = $1`, r.ID)
	return err
}

// cleanupExpiredReservations removes stale reservations.
func cleanupExpiredReservations(ctx context.Context) {
	if dbPool != nil {
		dbPool.Exec(ctx, `DELETE FROM upload_reservations WHERE expires_at <= NOW()`)
	}
}

var quotaGuard *QuotaGuard
