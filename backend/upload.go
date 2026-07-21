//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// UploadPipeline orchestrates the fail-closed ingestion of one asset: quota
// reservation, exclusive staging, hashing, content validation, malware
// scanning, no-replace promotion, catalog insertion, and audit — releasing the
// reservation and removing the staged file on any failure.
type UploadPipeline struct {
	storage *ConfinedStorage
	quota   *QuotaGuard
	scanner *ClamAVScanner
	staging StorageArea
	shared  StorageArea
}

// UploadRequest describes an ingestion.
type UploadRequest struct {
	UploaderID   string
	Filename     string
	Purpose      FilePurpose
	Visibility   FileVisibility
	DeclaredSize int64
	MaxBytes     int64
}

// UploadResult is the catalog outcome.
type UploadResult struct {
	FileID     string
	StorageKey string
	SHA256     string
	Size       int64
}

var (
	errUploadTooLarge = errors.New("upload_too_large")
	errUploadInfected = errors.New("upload_infected")
)

// Process runs the pipeline. body is streamed once into staging.
func (p *UploadPipeline) Process(ctx context.Context, req UploadRequest, body io.Reader) (UploadResult, error) {
	// 1. Reserve quota. Peak reserves the final size (same-filesystem promotion
	// via rename does not duplicate).
	reserveSize := req.DeclaredSize
	if reserveSize <= 0 {
		reserveSize = req.MaxBytes
	}
	res, err := p.quota.Reserve(ctx, req.UploaderID, reserveSize, reserveSize)
	if err != nil {
		return UploadResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = res.Release(ctx)
		}
	}()

	// 2. Stage into an exclusive file, hashing and bounding as we copy.
	stageKey := "ingest/" + randHexOrDie(16)
	f, err := p.storage.CreateExclusive(p.staging, stageKey, 0o600)
	if err != nil {
		return UploadResult{}, err
	}
	stagedRemoved := false
	removeStaged := func() {
		if !stagedRemoved {
			_ = p.storage.Remove(p.staging, stageKey)
			stagedRemoved = true
		}
	}
	defer func() {
		if !committed {
			removeStaged()
		}
	}()

	h := sha256.New()
	limit := req.MaxBytes
	written, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(&ctxReader{ctx: ctx, r: body}, limit+1))
	if copyErr != nil {
		f.Close()
		return UploadResult{}, copyErr
	}
	if written > limit {
		f.Close()
		return UploadResult{}, errUploadTooLarge
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return UploadResult{}, err
	}
	if err := f.Close(); err != nil {
		return UploadResult{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))

	// 3. Content validation (reopen the staged file as a ReaderAt).
	rf, err := p.storage.OpenRead(p.staging, stageKey)
	if err != nil {
		return UploadResult{}, err
	}
	_, verr := ValidateUploadContent(req.Filename, req.Purpose, rf, written)
	rf.Close()
	if verr != nil {
		return UploadResult{}, verr
	}

	// 4. Malware scan (reopen and stream).
	sf, err := p.storage.OpenRead(p.staging, stageKey)
	if err != nil {
		return UploadResult{}, err
	}
	clean, _, serr := p.scanner.Scan(ctx, sf)
	sf.Close()
	if serr != nil {
		return UploadResult{}, serr
	}
	if !clean {
		return UploadResult{}, errUploadInfected
	}

	// 5. Content-addressed promotion (no-replace). Sharding by hash prefix.
	finalKey := "files/" + sum[:2] + "/" + sum
	if _, err := p.storage.PromoteNoReplace(ctx, StorageRef{p.staging, stageKey}, StorageRef{p.shared, finalKey}, sum); err != nil {
		// A duplicate destination (same content already stored) is not fatal:
		// treat it as promoted.
		if err.Error() != "destination already exists" {
			return UploadResult{}, err
		}
	}
	stagedRemoved = true // promotion consumed the staged file

	// 6/7. Catalog row + audit in one transaction, then release the reservation.
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return UploadResult{}, err
	}
	defer tx.Rollback(ctx)
	vis := req.Visibility
	if vis == "" {
		vis = VisibilityCommunity
	}
	var fileID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type, purpose, visibility, state)
		VALUES ($1,$2,$3,'clean',$4,$5,$6,$7,$8,'available')
		RETURNING id::text
	`, req.Filename, sum, req.UploaderID, finalKey, written, mimeForName(req.Filename), string(req.Purpose), string(vis)).Scan(&fileID); err != nil {
		return UploadResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO file_audit (actor_id, file_id, action, detail)
		VALUES (NULLIF($1,'')::uuid, $2::uuid, 'create', jsonb_build_object('bytes', $3::bigint))
	`, req.UploaderID, fileID, written); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UploadResult{}, err
	}

	committed = true
	_ = res.Release(ctx)
	return UploadResult{FileID: fileID, StorageKey: finalKey, SHA256: sum, Size: written}, nil
}
