package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// File taxonomy types.
type (
	FilePurpose    string
	FileVisibility string
	FileState      string
	StorageArea    string
)

const (
	PurposeShared     FilePurpose = "shared"
	PurposeAvatar     FilePurpose = "avatar"
	PurposeBookIngest FilePurpose = "book_ingest"

	VisibilityPrivate   FileVisibility = "private"
	VisibilityCommunity FileVisibility = "community"

	StateAvailable   FileState = "available"
	StateQuarantined FileState = "quarantined"
	StateDeleted     FileState = "deleted"
)

// StoredFile is a catalog row.
type StoredFile struct {
	ID         string
	Filename   string
	SHA256     string
	UploaderID string
	StorageKey string
	MIMEType   string
	Purpose    FilePurpose
	Visibility FileVisibility
	State      FileState
	ScanStatus string
	SizeBytes  int64
	CreatedAt  time.Time
}

// FileStore is the purpose-aware catalog access layer. Every lookup names the
// expected purpose and current viewer so infected, private, avatar,
// book-ingestion, nonterminal, or another user's rows can never leak.
type FileStore struct{ db DBTX }

func newFileStore(db DBTX) *FileStore { return &FileStore{db: db} }

var errFileNotAvailable = errors.New("file not available")

// FindAvailable returns a file only when it is clean, available, of the
// expected purpose, and visible to the viewer (community, or the viewer's own
// private file).
func (s *FileStore) FindAvailable(ctx context.Context, id string, purpose FilePurpose, viewerID string) (StoredFile, error) {
	var f StoredFile
	err := s.db.QueryRow(ctx, `
		SELECT id::text, filename, sha256, COALESCE(uploader_id::text,''), storage_key, mime_type,
		       purpose, visibility, state, scan_status, size_bytes, created_at
		FROM files
		WHERE id = $1 AND purpose = $2 AND state = 'available' AND scan_status = 'clean'
		  AND (visibility = 'community' OR uploader_id = $3)
	`, id, string(purpose), viewerID).Scan(
		&f.ID, &f.Filename, &f.SHA256, &f.UploaderID, &f.StorageKey, &f.MIMEType,
		&f.Purpose, &f.Visibility, &f.State, &f.ScanStatus, &f.SizeBytes, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredFile{}, errFileNotAvailable
	}
	return f, err
}

// ListVisible returns available community shared files (plus the viewer's own
// private shared files) in descending create order, capped at limit. Infected,
// avatar, book-ingestion, and nonterminal rows are excluded.
func (s *FileStore) ListVisible(ctx context.Context, viewerID string, limit int) ([]StoredFile, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id::text, filename, sha256, COALESCE(uploader_id::text,''), storage_key, mime_type,
		       purpose, visibility, state, scan_status, size_bytes, created_at
		FROM files
		WHERE purpose = 'shared' AND state = 'available' AND scan_status = 'clean'
		  AND (visibility = 'community' OR uploader_id = $1)
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, viewerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredFile
	for rows.Next() {
		var f StoredFile
		if err := rows.Scan(&f.ID, &f.Filename, &f.SHA256, &f.UploaderID, &f.StorageKey, &f.MIMEType,
			&f.Purpose, &f.Visibility, &f.State, &f.ScanStatus, &f.SizeBytes, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecordAudit writes an immutable audit row.
func (s *FileStore) RecordAudit(ctx context.Context, actorID, fileID, folderID, action string, detail map[string]any) error {
	payload, _ := json.Marshal(detail)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO file_audit (actor_id, file_id, folder_id, action, detail)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5)
	`, actorID, fileID, folderID, action, payload)
	return err
}
