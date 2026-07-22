package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// marshalAuditDetail serializes an audit detail object, never emitting a NULL or
// non-object payload (the schema requires a JSON object).
func marshalAuditDetail(detail map[string]any) ([]byte, error) {
	payload, err := json.Marshal(detail)
	if err != nil || len(payload) == 0 || string(payload) == "null" {
		return []byte("{}"), err
	}
	return payload, nil
}

// RecordAudit writes an immutable audit row.
func (s *FileStore) RecordAudit(ctx context.Context, actorID, fileID, folderID, action string, detail map[string]any) error {
	payload, _ := marshalAuditDetail(detail)
	_, err := s.db.Exec(ctx, `
		INSERT INTO file_audit (actor_id, file_id, folder_id, action, detail)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5)
	`, actorID, fileID, folderID, action, payload)
	return err
}

// FileAuditEvent is one durable audit record.
type FileAuditEvent struct {
	ID        string         `json:"id"`
	ActorID   string         `json:"actorId,omitempty"`
	FileID    string         `json:"fileId,omitempty"`
	FolderID  string         `json:"folderId,omitempty"`
	Action    string         `json:"action"`
	Detail    map[string]any `json:"detail"`
	CreatedAt time.Time      `json:"createdAt"`
}

// ListAudit returns a stable descending (created_at, id) page of audit events.
// The seek tuple is carried in the opaque cursor by the caller.
func (s *FileStore) ListAudit(ctx context.Context, seekTime time.Time, seekID string, haveSeek bool, limit int) ([]FileAuditEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id::text, COALESCE(actor_id::text,''), COALESCE(file_id::text,''),
		       COALESCE(folder_id::text,''), action, detail, created_at
		FROM file_audit
		WHERE ($1::boolean IS FALSE) OR (created_at, id) < ($2::timestamptz, $3::uuid)
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, haveSeek, seekTime, seekIDArg(seekID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileAuditEvent{}
	for rows.Next() {
		var e FileAuditEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.FileID, &e.FolderID, &e.Action, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// handleListFileAudit serves the durable file/folder audit log as a stable,
// cursor-paginated descending (created_at, id) list for moderators.
func handleListFileAudit(w http.ResponseWriter, r *http.Request) {
	req, _, err := resolvePageRequest("files.audit", r.URL.Query().Get("sort"),
		r.URL.Query().Get("cursor"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The list request is invalid.")
		return
	}

	var seekTime time.Time
	var seekID string
	haveSeek := false
	if req.After != "" {
		c, derr := cursorCodec.Decode("files.audit", req.After)
		if derr != nil || len(c.Values) != 2 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid.")
			return
		}
		if t, terr := time.Parse(time.RFC3339Nano, c.Values[0].Value); terr == nil {
			seekTime = t
			seekID = c.Values[1].Value
			haveSeek = true
		}
	}

	store := newFileStore(dbPool)
	events, err := store.ListAudit(r.Context(), seekTime, seekID, haveSeek, req.Limit+1)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	page := Page[FileAuditEvent]{Items: events}
	if len(events) > req.Limit {
		page.Items = events[:req.Limit]
		last := page.Items[req.Limit-1]
		next, encErr := cursorCodec.Encode("files.audit", PageCursor{
			Sort:     req.Sort,
			Values:   []CursorValue{{Kind: "time", Value: last.CreatedAt.Format(time.RFC3339Nano)}, {Kind: "uuid", Value: last.ID}},
			ID:       last.ID,
			IssuedAt: time.Now(),
		})
		if encErr == nil {
			page.NextCursor = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}
