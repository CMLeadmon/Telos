package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// logReconcileError records a reconciliation failure without leaking storage
// paths or upstream detail into request-facing output.
func logReconcileError(err error) {
	log.Printf("file reconciler: pass failed: %v", err)
}

// reconcileWalkerLock is the session advisory key that admits exactly one
// reconciliation walker per node; a second caller finds it held and yields.
const reconcileWalkerLock int64 = 0x54_4C_4F_53_52 // "TLOSR"

// Bounds keep every run cheap and interruptible.
const (
	reconcileBatch    = 100 // rows claimed per category per run
	reconcileDeadline = 30 * time.Second
	orphanMinAge      = 30 * time.Minute // twice the 15-minute ingestion lease
)

// ReconcileReport is the bounded outcome of a single reconciliation pass.
type ReconcileReport struct {
	Missing, Orphaned, Quarantined int
	Repaired, Deleted, Consumed    int
	Scanned, Deferred              int
	NextCursor                     string
}

// PhysicalChecker abstracts confined physical storage so reconciliation is
// testable and degrades safely when storage is unconfigured.
type PhysicalChecker interface {
	// Exists reports whether the content key is physically present.
	Exists(area StorageArea, key string) (bool, error)
	// HashAndSize returns the content hash and size of a present key.
	HashAndSize(area StorageArea, key string) (sha256 string, size int64, err error)
	// Remove deletes a content key; absence is not an error.
	Remove(area StorageArea, key string) error
}

// BookCatalog acknowledges Grimmory ingestion: a delivered drop file becomes
// consumed only once the catalog reports the imported book.
type BookCatalog interface {
	// LookupImported returns the catalog book ID for a delivered content hash,
	// or found=false when the import has not yet been observed.
	LookupImported(ctx context.Context, sha256 string) (bookID string, found bool, err error)
}

// FileReconciler converges database lifecycle rows with physical/catalog state.
type FileReconciler struct {
	pool    *pgxpool.Pool
	storage PhysicalChecker
	catalog BookCatalog
}

func newFileReconciler(pool *pgxpool.Pool, storage PhysicalChecker, catalog BookCatalog) *FileReconciler {
	return &FileReconciler{pool: pool, storage: storage, catalog: catalog}
}

// RunOnce performs one bounded pass under the single-walker lock. It is safe to
// run at startup and periodically; a second identical pass converges with no new
// mutation or audit.
func (r *FileReconciler) RunOnce(ctx context.Context) (ReconcileReport, error) {
	ctx, cancel := context.WithTimeout(ctx, reconcileDeadline)
	defer cancel()

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return ReconcileReport{}, err
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", reconcileWalkerLock).Scan(&got); err != nil {
		return ReconcileReport{}, err
	}
	if !got {
		// Another walker owns this node; yield without touching any row.
		return ReconcileReport{Deferred: 1}, nil
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", reconcileWalkerLock)

	var rep ReconcileReport
	if err := r.finalizeDeletions(ctx, &rep); err != nil {
		return rep, err
	}
	if err := r.resolveExpiredLeases(ctx, &rep); err != nil {
		return rep, err
	}
	if err := r.acknowledgeHandoffs(ctx, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// finalizeDeletions removes the physical asset for each file in state 'deleting'
// and marks it 'deleted' only after verified absence. Idempotent under retry.
func (r *FileReconciler) finalizeDeletions(ctx context.Context, rep *ReconcileReport) error {
	if r.storage == nil {
		// Without confined storage we cannot prove physical absence, and must
		// never mark a row deleted on faith. Defer to a run that has storage.
		return nil
	}
	return r.claim(ctx, `
		SELECT id::text, storage_key, purpose FROM files
		WHERE state = 'deleting'
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, func(tx pgx.Tx, id, key, purpose string) error {
		rep.Scanned++
		area := areaForPurpose(purpose)
		if r.storage != nil {
			if err := r.storage.Remove(area, key); err != nil {
				return err
			}
			present, err := r.storage.Exists(area, key)
			if err != nil {
				return err
			}
			if present {
				// Removal did not take effect; leave 'deleting' for next pass.
				return nil
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET state = 'deleted' WHERE id = $1`, id); err != nil {
			return err
		}
		rep.Deleted++
		return recordAuditTx(ctx, tx, "", id, "", "reconcile", map[string]any{"outcome": "deleted"})
	})
}

// resolveExpiredLeases repairs or fails nonterminal ingestion rows whose lease
// has expired. A present asset whose hash/size prove identity is promoted to
// available; otherwise the row is marked missing.
func (r *FileReconciler) resolveExpiredLeases(ctx context.Context, rep *ReconcileReport) error {
	return r.claimLease(ctx, `
		SELECT f.id::text, f.storage_key, f.purpose, f.sha256, f.size_bytes
		FROM file_ingestion_leases l
		JOIN files f ON f.id = l.file_id
		WHERE l.expires_at < NOW() - $2::interval
		  AND f.state IN ('staged','validating','scanning','promoting')
		ORDER BY l.expires_at
		FOR UPDATE OF l SKIP LOCKED
		LIMIT $1
	`, func(tx pgx.Tx, id, key, purpose, wantHash string, wantSize int64) error {
		rep.Scanned++
		repaired := false
		if r.storage != nil && wantHash != "" {
			present, err := r.storage.Exists(areaForPurpose(purpose), key)
			if err != nil {
				return err
			}
			if present {
				gotHash, gotSize, err := r.storage.HashAndSize(areaForPurpose(purpose), key)
				if err != nil {
					return err
				}
				repaired = gotHash == wantHash && gotSize == wantSize
			}
		}
		if repaired {
			if _, err := tx.Exec(ctx, `UPDATE files SET state = 'available' WHERE id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM file_ingestion_leases WHERE file_id = $1`, id); err != nil {
				return err
			}
			rep.Repaired++
			return recordAuditTx(ctx, tx, "", id, "", "reconcile", map[string]any{"outcome": "repaired"})
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET state = 'missing' WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM file_ingestion_leases WHERE file_id = $1`, id); err != nil {
			return err
		}
		rep.Missing++
		return recordAuditTx(ctx, tx, "", id, "", "missing", map[string]any{"reason": "lease_expired"})
	})
}

// acknowledgeHandoffs converts delivered (handed_off) book drops to consumed
// once the Grimmory catalog confirms the import; unexplained absence stays
// unresolved (never assumed success) until the catalog observes it.
func (r *FileReconciler) acknowledgeHandoffs(ctx context.Context, rep *ReconcileReport) error {
	if r.catalog == nil {
		return nil
	}
	return r.claim(ctx, `
		SELECT id::text, sha256, '' FROM files
		WHERE state = 'handed_off'
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, func(tx pgx.Tx, id, hash, _ string) error {
		rep.Scanned++
		bookID, found, err := r.catalog.LookupImported(ctx, hash)
		if err != nil {
			return err
		}
		if !found {
			// Not yet imported; leave for a later pass rather than assume loss.
			rep.Deferred++
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET state = 'consumed' WHERE id = $1`, id); err != nil {
			return err
		}
		rep.Consumed++
		return recordAuditTx(ctx, tx, "", id, "", "consume", map[string]any{"bookId": bookID})
	})
}

// claim runs a three-column claim query in one transaction and applies fn to
// each row. The batch bound comes from reconcileBatch.
func (r *FileReconciler) claim(ctx context.Context, query string, fn func(tx pgx.Tx, a, b, c string) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, query, reconcileBatch)
	if err != nil {
		return err
	}
	type triple struct{ a, b, c string }
	var claimed []triple
	for rows.Next() {
		var t triple
		if err := rows.Scan(&t.a, &t.b, &t.c); err != nil {
			rows.Close()
			return err
		}
		claimed = append(claimed, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range claimed {
		if err := fn(tx, t.a, t.b, t.c); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// claimLease runs the expired-lease claim (five columns) in one transaction.
func (r *FileReconciler) claimLease(ctx context.Context, query string, fn func(tx pgx.Tx, id, key, purpose, hash string, size int64) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, query, reconcileBatch, orphanMinAge.String())
	if err != nil {
		return err
	}
	type rec struct {
		id, key, purpose, hash string
		size                   int64
	}
	var claimed []rec
	for rows.Next() {
		var c rec
		if err := rows.Scan(&c.id, &c.key, &c.purpose, &c.hash, &c.size); err != nil {
			rows.Close()
			return err
		}
		claimed = append(claimed, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range claimed {
		if err := fn(tx, c.id, c.key, c.purpose, c.hash, c.size); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// areaForPurpose maps a file purpose to its storage area. Book ingestion stages
// into the bookdrop; everything else lives under the shared root.
func areaForPurpose(purpose string) StorageArea {
	switch FilePurpose(purpose) {
	case PurposeBookIngest:
		return StorageArea("bookdrop")
	case PurposeAvatar:
		return StorageArea("avatars")
	default:
		return StorageArea("shared")
	}
}

var errReconcilerBusy = errors.New("reconciler already running on this node")

// fileReconciler is the process-wide reconciler; nil until wired at startup.
var fileReconciler *FileReconciler

// reconcileInterval paces periodic convergence passes.
const reconcileInterval = 5 * time.Minute

// StartFileReconciler runs one pass immediately and then every
// reconcileInterval until ctx is cancelled. Passes are bounded and idempotent,
// and a second walker on the node yields, so overlapping ticks are harmless.
func StartFileReconciler(ctx context.Context, r *FileReconciler) {
	if r == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		run := func() {
			if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
				logReconcileError(err)
			}
		}
		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
