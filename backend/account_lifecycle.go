package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeletionReceipt is the idempotent record of a completed account deletion.
type DeletionReceipt struct {
	RequestID   string
	UserID      string
	CompletedAt time.Time
}

// AssetRemover removes a physical asset (avatar or private upload) by storage
// key. It must be safe to call more than once for the same key.
type AssetRemover interface {
	Remove(ctx context.Context, area, storageKey string) error
}

// noopAssetRemover is the default when no filesystem remover is wired (tests
// override it; production wires a filesystem-backed remover in Phase 4).
type noopAssetRemover struct{}

func (noopAssetRemover) Remove(context.Context, string, string) error { return nil }

var assetRemover AssetRemover = noopAssetRemover{}

// DeleteAccount performs a complete, idempotent account deletion. In one
// transaction it revokes sessions and owned invites, deletes private state,
// anonymizes retained public authorship, enqueues the account-deletion
// security event and physical-asset jobs, and records a receipt. Sockets are
// closed before success is reported, and asset jobs run after commit.
func DeleteAccount(ctx context.Context, userID string) (DeletionReceipt, error) {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return DeletionReceipt{}, err
	}
	defer tx.Rollback(ctx)

	// Idempotency: if already deleted, return the existing receipt.
	var existing DeletionReceipt
	err = tx.QueryRow(ctx, `SELECT request_id::text, user_id::text, completed_at FROM deletion_requests WHERE user_id = $1`, userID).
		Scan(&existing.RequestID, &existing.UserID, &existing.CompletedAt)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DeletionReceipt{}, err
	}

	// Last-Owner protection also applies to self/admin deletion.
	var isOwner bool
	tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role_id='Owner')`, userID).Scan(&isOwner)
	if isOwner {
		n, lerr := lockOwnerMembership(ctx, tx)
		if lerr != nil {
			return DeletionReceipt{}, lerr
		}
		if n <= 1 {
			return DeletionReceipt{}, errLastOwner
		}
	}

	// Collect private-asset storage keys before deleting the rows.
	type asset struct{ area, key string }
	var assets []asset
	rows, err := tx.Query(ctx, `SELECT purpose, storage_key FROM files WHERE uploader_id = $1 AND purpose <> 'shared'`, userID)
	if err != nil {
		return DeletionReceipt{}, err
	}
	for rows.Next() {
		var purpose, key string
		rows.Scan(&purpose, &key)
		area := "upload"
		if purpose == "avatar" {
			area = "avatar"
		}
		assets = append(assets, asset{area: area, key: key})
	}
	rows.Close()

	// Delete durable private state.
	for _, stmt := range []string{
		`UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`,
		`UPDATE invites SET used_at = COALESCE(used_at, NOW()) WHERE creator_id = $1 AND used_at IS NULL`,
		`DELETE FROM user_preferences WHERE user_id = $1`,
		`DELETE FROM book_progress WHERE user_id = $1`,
		`DELETE FROM message_reactions WHERE user_id = $1`,
		`DELETE FROM channel_reads WHERE user_id = $1`,
		`DELETE FROM notifications WHERE user_id = $1`,
		`DELETE FROM user_events WHERE recipient_id = $1`,
		`DELETE FROM annotations WHERE user_id = $1 AND visibility = 'private'`,
		`DELETE FROM media_list_entries WHERE user_id = $1`,
		`DELETE FROM media_lists WHERE user_id = $1`,
		`DELETE FROM files WHERE uploader_id = $1 AND purpose <> 'shared'`,
		`DELETE FROM user_roles WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return DeletionReceipt{}, err
		}
	}

	// End hosted Watch Parties and purge the user's party references.
	if err := PurgeUserWatchParties(ctx, tx, userID); err != nil {
		return DeletionReceipt{}, err
	}

	// Anonymize the user row; public chat authorship is retained as
	// "Deleted User" via the surviving (anonymized) users row.
	suffix := userID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET username = 'deleted-' || $2, display_name = 'Deleted User',
			password_hash = '!', active = FALSE, avatar_file_id = NULL
		WHERE id = $1
	`, userID, suffix); err != nil {
		return DeletionReceipt{}, err
	}

	// Enqueue physical-asset deletion jobs.
	for _, a := range assets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_deletion_jobs (user_id, area, storage_key) VALUES ($1, $2, $3)
		`, userID, a.area, a.key); err != nil {
			return DeletionReceipt{}, err
		}
	}

	// Durable account-deletion security event.
	if err := securityEvents.Record(ctx, tx, SecurityEventIntent{Kind: "account_deleted", ActorID: userID, SubjectID: userID}); err != nil {
		return DeletionReceipt{}, err
	}

	// Idempotent receipt.
	var receipt DeletionReceipt
	if err := tx.QueryRow(ctx, `
		INSERT INTO deletion_requests (user_id) VALUES ($1)
		RETURNING request_id::text, user_id::text, completed_at
	`, userID).Scan(&receipt.RequestID, &receipt.UserID, &receipt.CompletedAt); err != nil {
		return DeletionReceipt{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DeletionReceipt{}, err
	}

	// Close live sockets before reporting success.
	revokeUserSockets(userID)

	// Run asset deletion jobs after commit (best-effort; reconciliation retries
	// any that fail).
	go runAssetDeletionJobs(context.Background(), userID)

	return receipt, nil
}

// runAssetDeletionJobs removes physical assets and marks jobs done. Failures
// remain pending for the reconciliation pass.
func runAssetDeletionJobs(ctx context.Context, userID string) {
	rows, err := dbPool.Query(ctx, `SELECT id::text, area, storage_key FROM asset_deletion_jobs WHERE user_id = $1 AND status = 'pending'`, userID)
	if err != nil {
		return
	}
	type job struct{ id, area, key string }
	var jobs []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.area, &j.key)
		jobs = append(jobs, j)
	}
	rows.Close()
	for _, j := range jobs {
		if err := assetRemover.Remove(ctx, j.area, j.key); err != nil {
			dbPool.Exec(ctx, `UPDATE asset_deletion_jobs SET attempts = attempts + 1 WHERE id = $1`, j.id)
			log.Printf("asset deletion job %s failed: %v", j.id, err)
			continue
		}
		dbPool.Exec(ctx, `UPDATE asset_deletion_jobs SET status='done', completed_at=NOW(), attempts=attempts+1 WHERE id=$1`, j.id)
	}
}
