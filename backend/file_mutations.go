package main

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FileService is the authorized, row-locked mutation surface for the logical
// file/folder tree. Physical content keys are immutable: rename and move change
// only the database logical parent/name. Every mutation is idempotent under
// retry and writes a durable audit row.
type FileService struct {
	pool  *pgxpool.Pool
	store *FileStore
}

// LogicalFolder is a database-only tree node (physical storage never mirrors
// user path names).
type LogicalFolder struct {
	ID          string
	ParentID    string
	OwnerID     string
	DisplayName string
	Visibility  FileVisibility
}

var (
	errFolderNotEmpty  = errors.New("folder not empty")
	errFolderNotFound  = errors.New("folder not found")
	errFileNotFound    = errors.New("file not found")
	errNameInvalid     = errors.New("name is invalid")
	errSiblingConflict = errors.New("a sibling with that name already exists")
	errFolderCycle     = errors.New("move would create a folder cycle")
	errMutationDenied  = errors.New("mutation not authorized")
)

// fileService is the process-wide mutation service; nil until wired at startup.
var fileService *FileService

func newFileService(pool *pgxpool.Pool) *FileService {
	return &FileService{pool: pool, store: newFileStore(pool)}
}

// normalizeFolderName folds a display name to its canonical sibling key: NFC-ish
// trimming, collapsing internal whitespace, and lowercasing. It rejects empty,
// overlong, control-character, and path-separator names so a logical name can
// never imply a physical path.
func normalizeFolderName(display string) (norm, clean string, err error) {
	clean = strings.TrimSpace(display)
	if clean == "" || len(clean) > 255 {
		return "", "", errNameInvalid
	}
	for _, r := range clean {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return "", "", errNameInvalid
		}
	}
	// Collapse runs of whitespace to a single space for the normalized key.
	norm = strings.ToLower(strings.Join(strings.Fields(clean), " "))
	if norm == "" || norm == "." || norm == ".." {
		return "", "", errNameInvalid
	}
	return norm, clean, nil
}

// authorizeOwner allows a mutation when the actor owns the resource, or holds
// manage_files for structure they do not own (including shared-root nodes with
// no owner).
func (s *FileService) authorizeOwner(ctx context.Context, actor *UserContext, ownerID string) error {
	if actor == nil {
		return errMutationDenied
	}
	if ownerID != "" && ownerID == actor.ID {
		return nil
	}
	ok, err := hasPermission(ctx, actor, "manage_files", nil)
	if err != nil {
		return err
	}
	if !ok {
		return errMutationDenied
	}
	return nil
}

// CreateFolder inserts a logical folder under an optional parent. The actor must
// own the parent (or hold manage_files); normalized sibling collisions fail with
// errSiblingConflict.
func (s *FileService) CreateFolder(ctx context.Context, actor *UserContext, parentID, name string) (LogicalFolder, error) {
	norm, clean, err := normalizeFolderName(name)
	if err != nil {
		return LogicalFolder{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LogicalFolder{}, err
	}
	defer tx.Rollback(ctx)

	visibility := VisibilityCommunity
	if parentID != "" {
		var parentOwner, parentVis string
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(owner_id::text,''), visibility
			FROM logical_folders WHERE id = $1 FOR UPDATE
		`, parentID).Scan(&parentOwner, &parentVis)
		if errors.Is(err, pgx.ErrNoRows) {
			return LogicalFolder{}, errFolderNotFound
		}
		if err != nil {
			return LogicalFolder{}, err
		}
		if err := s.authorizeOwner(ctx, actor, parentOwner); err != nil {
			return LogicalFolder{}, err
		}
		visibility = FileVisibility(parentVis)
	}
	// A root-level folder (no parent) is owned by its creator, so no further
	// authorization is required; nesting is gated on the parent above.
	if actor == nil {
		return LogicalFolder{}, errMutationDenied
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO logical_folders (parent_id, owner_id, normalized_name, display_name, visibility)
		VALUES (NULLIF($1,'')::uuid, $2::uuid, $3, $4, $5)
		RETURNING id::text
	`, parentID, actor.ID, norm, clean, string(visibility)).Scan(&id)
	if isUniqueViolation(err) {
		return LogicalFolder{}, errSiblingConflict
	}
	if err != nil {
		return LogicalFolder{}, err
	}
	if err := recordAuditTx(ctx, tx, actor.ID, "", id, "create", map[string]any{"kind": "folder"}); err != nil {
		return LogicalFolder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LogicalFolder{}, err
	}
	return LogicalFolder{ID: id, ParentID: parentID, OwnerID: actor.ID, DisplayName: clean, Visibility: visibility}, nil
}

// RenameFolder changes only the display/normalized name under a row lock.
func (s *FileService) RenameFolder(ctx context.Context, actor *UserContext, folderID, name string) error {
	norm, clean, err := normalizeFolderName(name)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, err := lockFolderOwner(ctx, tx, folderID)
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE logical_folders SET normalized_name=$2, display_name=$3 WHERE id=$1`, folderID, norm, clean)
		if isUniqueViolation(err) {
			return errSiblingConflict
		}
		if err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, "", folderID, "rename", map[string]any{"kind": "folder"})
	})
}

// MoveFolder reparents a folder, rejecting self/descendant targets (cycles) and
// normalized sibling collisions.
func (s *FileService) MoveFolder(ctx context.Context, actor *UserContext, folderID, parentID string) error {
	if folderID == parentID {
		return errFolderCycle
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, err := lockFolderOwner(ctx, tx, folderID)
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		if parentID != "" {
			targetOwner, err := lockFolderOwner(ctx, tx, parentID)
			if err != nil {
				return err
			}
			if err := s.authorizeOwner(ctx, actor, targetOwner); err != nil {
				return err
			}
			// Reject moving a folder beneath itself or one of its descendants.
			var cycle bool
			if err := tx.QueryRow(ctx, `
				WITH RECURSIVE descendants AS (
					SELECT id FROM logical_folders WHERE id = $1
					UNION ALL
					SELECT f.id FROM logical_folders f JOIN descendants d ON f.parent_id = d.id
				)
				SELECT EXISTS(SELECT 1 FROM descendants WHERE id = $2)
			`, folderID, parentID).Scan(&cycle); err != nil {
				return err
			}
			if cycle {
				return errFolderCycle
			}
		}
		_, err = tx.Exec(ctx, `UPDATE logical_folders SET parent_id = NULLIF($2,'')::uuid WHERE id = $1`, folderID, parentID)
		if isUniqueViolation(err) {
			return errSiblingConflict
		}
		if err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, "", folderID, "move", map[string]any{"parent": parentID})
	})
}

// DeleteFolder removes an empty folder; a folder with any live child folder or
// non-deleted file fails with errFolderNotEmpty (409).
func (s *FileService) DeleteFolder(ctx context.Context, actor *UserContext, folderID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, err := lockFolderOwner(ctx, tx, folderID)
		if errors.Is(err, errFolderNotFound) {
			return nil // idempotent: already gone
		}
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		var children int
		if err := tx.QueryRow(ctx, `
			SELECT
			  (SELECT COUNT(*) FROM logical_folders WHERE parent_id = $1)
			+ (SELECT COUNT(*) FROM files WHERE folder_id = $1 AND state NOT IN ('deleted','deleting','consumed'))
		`, folderID).Scan(&children); err != nil {
			return err
		}
		if children > 0 {
			return errFolderNotEmpty
		}
		if _, err := tx.Exec(ctx, `DELETE FROM logical_folders WHERE id = $1`, folderID); err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, "", folderID, "delete", map[string]any{"kind": "folder"})
	})
}

// RenameFile changes only the logical name; the physical content key is never
// touched.
func (s *FileService) RenameFile(ctx context.Context, actor *UserContext, fileID, name string) error {
	_, clean, err := normalizeFolderName(name)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, _, err := lockFileOwnerState(ctx, tx, fileID)
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET logical_name = $2 WHERE id = $1`, fileID, clean); err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, fileID, "", "rename", map[string]any{"kind": "file"})
	})
}

// MoveFile changes only the logical parent folder; the physical key is immutable.
func (s *FileService) MoveFile(ctx context.Context, actor *UserContext, fileID, folderID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, _, err := lockFileOwnerState(ctx, tx, fileID)
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		if folderID != "" {
			targetOwner, err := lockFolderOwner(ctx, tx, folderID)
			if err != nil {
				return err
			}
			if err := s.authorizeOwner(ctx, actor, targetOwner); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET folder_id = NULLIF($2,'')::uuid WHERE id = $1`, fileID, folderID); err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, fileID, "", "move", map[string]any{"folder": folderID})
	})
}

// DeleteFile atomically transitions an available file to deleting and hides it.
// Verified physical absence (by the reconciler) is what later permits deleted.
// Repeated deletion of an already-deleting/deleted file is a stable no-op.
func (s *FileService) DeleteFile(ctx context.Context, actor *UserContext, fileID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		owner, state, err := lockFileOwnerState(ctx, tx, fileID)
		if errors.Is(err, errFileNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.authorizeOwner(ctx, actor, owner); err != nil {
			return err
		}
		if state == "deleting" || state == "deleted" {
			return nil // idempotent
		}
		if _, err := tx.Exec(ctx, `UPDATE files SET state = 'deleting' WHERE id = $1`, fileID); err != nil {
			return err
		}
		return recordAuditTx(ctx, tx, actor.ID, fileID, "", "delete", map[string]any{"prev": state})
	})
}

// --- shared helpers ---

func (s *FileService) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockFolderOwner(ctx context.Context, tx pgx.Tx, folderID string) (string, error) {
	var owner string
	err := tx.QueryRow(ctx, `SELECT COALESCE(owner_id::text,'') FROM logical_folders WHERE id = $1 FOR UPDATE`, folderID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errFolderNotFound
	}
	return owner, err
}

func lockFileOwnerState(ctx context.Context, tx pgx.Tx, fileID string) (owner, state string, err error) {
	err = tx.QueryRow(ctx, `SELECT COALESCE(uploader_id::text,''), state FROM files WHERE id = $1 FOR UPDATE`, fileID).Scan(&owner, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errFileNotFound
	}
	return owner, state, err
}

// recordAuditTx writes an immutable audit row inside a caller transaction.
func recordAuditTx(ctx context.Context, tx pgx.Tx, actorID, fileID, folderID, action string, detail map[string]any) error {
	payload, _ := marshalAuditDetail(detail)
	_, err := tx.Exec(ctx, `
		INSERT INTO file_audit (actor_id, file_id, folder_id, action, detail)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5)
	`, actorID, fileID, folderID, action, payload)
	return err
}
