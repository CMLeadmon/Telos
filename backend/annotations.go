package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// PDFRect is a normalized (0..1) highlight rectangle on a PDF page.
type PDFRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// AnnotationLocator anchors an annotation in an EPUB (CFI) or a PDF (page +
// rectangles).
type AnnotationLocator struct {
	Kind  string    `json:"kind"`
	CFI   string    `json:"cfi,omitempty"`
	Page  int       `json:"page,omitempty"`
	Rects []PDFRect `json:"rects,omitempty"`
}

const (
	maxSelectedTextRunes = 2000
	maxNoteRunes         = 10000
	maxReplyRunes        = 4000
	maxAnnotationCFI     = 1024
	maxPDFRects          = 64
)

var (
	errAnnotationLocator  = errors.New("annotation locator invalid")
	errAnnotationText     = errors.New("annotation text exceeds limits")
	errAnnotationDenied   = errors.New("annotation not authorized")
	errAnnotationNotFound = errors.New("annotation not found")
	errReplyOnlyCommunity = errors.New("replies are only allowed on community annotations")
)

// validateAnnotationLocator validates and canonicalizes a locator for a book
// format. EPUB requires a bounded CFI; PDF requires page>=1 and 1..64 normalized
// rectangles.
func validateAnnotationLocator(format string, raw json.RawMessage) (json.RawMessage, error) {
	var loc AnnotationLocator
	if err := json.Unmarshal(raw, &loc); err != nil {
		return nil, errAnnotationLocator
	}
	switch formatKindBackend(format) {
	case "epub":
		if loc.CFI == "" || len(loc.CFI) > maxAnnotationCFI || loc.Page != 0 || len(loc.Rects) != 0 {
			return nil, errAnnotationLocator
		}
		for _, r := range loc.CFI {
			if r < 0x20 {
				return nil, errAnnotationLocator
			}
		}
		out, _ := json.Marshal(AnnotationLocator{Kind: "epub", CFI: loc.CFI})
		return out, nil
	case "pdf":
		if loc.Page < 1 || loc.Page > 100000 || loc.CFI != "" {
			return nil, errAnnotationLocator
		}
		if len(loc.Rects) < 1 || len(loc.Rects) > maxPDFRects {
			return nil, errAnnotationLocator
		}
		for _, rc := range loc.Rects {
			if !normalized(rc.X) || !normalized(rc.Y) || !normalized(rc.W) || !normalized(rc.H) || rc.W <= 0 || rc.H <= 0 || rc.X+rc.W > 1.0001 || rc.Y+rc.H > 1.0001 {
				return nil, errAnnotationLocator
			}
		}
		out, _ := json.Marshal(AnnotationLocator{Kind: "pdf", Page: loc.Page, Rects: loc.Rects})
		return out, nil
	default:
		return nil, errAnnotationLocator
	}
}

func normalized(f float64) bool { return f >= 0 && f <= 1 }

// formatKindBackend mirrors the frontend reader.formatKind mapping.
func formatKindBackend(format string) string {
	switch {
	case eqFold(format, "epub"):
		return "epub"
	case eqFold(format, "pdf"):
		return "pdf"
	default:
		return ""
	}
}

func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// validateAnnotationText enforces the selected-text and note code-point limits.
func validateAnnotationText(selected, note string) error {
	if utf8.RuneCountInString(selected) > maxSelectedTextRunes || utf8.RuneCountInString(note) > maxNoteRunes {
		return errAnnotationText
	}
	return nil
}

// ── Store ───────────────────────────────────────────────────────────────────

// Annotation is one piece of commentary anchored to a media target. For a book
// the locator/selectedText carry the highlight; for streamed media or a file the
// locator is empty and the annotation is a plain comment.
type Annotation struct {
	ID           string          `json:"id"`
	TargetType   string          `json:"targetType"`
	TargetID     string          `json:"targetId"`
	OwnerID      string          `json:"ownerId"`
	Visibility   string          `json:"visibility"`
	Locator      json.RawMessage `json:"locator"`
	SelectedText string          `json:"selectedText"`
	Note         string          `json:"note"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// annotationTargetTypes are the media kinds an annotation may anchor to.
var annotationTargetTypes = map[string]bool{"book": true, "media": true, "file": true}

// AnnotationReply is one community reply on an annotation.
type AnnotationReply struct {
	ID           string    `json:"id"`
	AnnotationID string    `json:"annotationId"`
	AuthorID     string    `json:"authorId"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CreateAnnotation inserts a new annotation on a target (default private).
func CreateAnnotation(ctx context.Context, targetType, targetID, ownerID, visibility string, locator json.RawMessage, selected, note string) (Annotation, error) {
	if !annotationTargetTypes[targetType] {
		return Annotation{}, errAnnotationLocator
	}
	if visibility != "private" && visibility != "community" {
		visibility = "private"
	}
	if err := validateAnnotationText(selected, note); err != nil {
		return Annotation{}, err
	}
	var a Annotation
	err := dbPool.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, locator, selected_text, note)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, $7)
		RETURNING id::text, target_type, target_id, user_id::text, visibility, locator, selected_text, note, created_at, updated_at
	`, targetType, targetID, ownerID, visibility, locator, selected, note).Scan(
		&a.ID, &a.TargetType, &a.TargetID, &a.OwnerID, &a.Visibility, &a.Locator, &a.SelectedText, &a.Note, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// ListAnnotations returns the viewer's own annotations plus community ones for a
// target, newest first. Another user's private annotation is never returned.
func ListAnnotations(ctx context.Context, targetType, targetID, viewerID string, limit int) ([]Annotation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := dbPool.Query(ctx, `
		SELECT id::text, target_type, target_id, user_id::text, visibility, locator, selected_text, note, created_at, updated_at
		FROM annotations
		WHERE target_type = $1 AND target_id = $2 AND (visibility = 'community' OR user_id = $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, targetType, targetID, viewerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.TargetType, &a.TargetID, &a.OwnerID, &a.Visibility, &a.Locator, &a.SelectedText, &a.Note, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAnnotation lets the owner change the note and visibility.
func UpdateAnnotation(ctx context.Context, id, ownerID, visibility, note string) error {
	if visibility != "private" && visibility != "community" {
		return errAnnotationLocator
	}
	if err := validateAnnotationText("", note); err != nil {
		return err
	}
	tag, err := dbPool.Exec(ctx, `
		UPDATE annotations SET note = $3, visibility = $4, updated_at = now()
		WHERE id = $1 AND user_id = $2
	`, id, ownerID, note, visibility)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errAnnotationNotFound
	}
	return nil
}

// DeleteAnnotation removes an annotation. The owner may always delete; a
// moderator may delete a community annotation (recording an audit event).
func DeleteAnnotation(ctx context.Context, id string, actor *UserContext) error {
	var ownerID, visibility string
	err := dbPool.QueryRow(ctx, `SELECT user_id::text, visibility FROM annotations WHERE id = $1`, id).Scan(&ownerID, &visibility)
	if errors.Is(err, pgx.ErrNoRows) {
		return errAnnotationNotFound
	}
	if err != nil {
		return err
	}
	if ownerID == actor.ID {
		_, err = dbPool.Exec(ctx, `DELETE FROM annotations WHERE id = $1`, id)
		return err
	}
	// Not the owner: require moderate_annotations and a community target.
	can, perr := hasPermission(ctx, actor, "moderate_annotations", nil)
	if perr != nil {
		return perr
	}
	if !can || visibility != "community" {
		return errAnnotationDenied
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM annotations WHERE id = $1`, id); err != nil {
		return err
	}
	if err := securityEvents.Record(ctx, tx, SecurityEventIntent{Kind: "annotation_moderated", ActorID: actor.ID, SubjectID: ownerID, ResourceID: id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateReply adds a community reply and notifies the annotation owner. Replies
// are only allowed on community annotations.
func CreateReply(ctx context.Context, annotationID, authorID, body string) (AnnotationReply, error) {
	if utf8.RuneCountInString(body) == 0 || utf8.RuneCountInString(body) > maxReplyRunes {
		return AnnotationReply{}, errAnnotationText
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return AnnotationReply{}, err
	}
	defer tx.Rollback(ctx)

	var ownerID, visibility, targetType, targetID string
	err = tx.QueryRow(ctx, `SELECT user_id::text, visibility, target_type, target_id FROM annotations WHERE id = $1 FOR UPDATE`, annotationID).Scan(&ownerID, &visibility, &targetType, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AnnotationReply{}, errAnnotationNotFound
	}
	if err != nil {
		return AnnotationReply{}, err
	}
	if visibility != "community" {
		return AnnotationReply{}, errReplyOnlyCommunity
	}

	var reply AnnotationReply
	err = tx.QueryRow(ctx, `
		INSERT INTO annotation_replies (annotation_id, user_id, body)
		VALUES ($1, $2::uuid, $3)
		RETURNING id::text, annotation_id::text, user_id::text, body, created_at
	`, annotationID, authorID, body).Scan(&reply.ID, &reply.AnnotationID, &reply.AuthorID, &reply.Body, &reply.CreatedAt)
	if err != nil {
		return AnnotationReply{}, err
	}

	// Notify the annotation owner (idempotent by reply id); payload carries IDs
	// and actor only — never the reply body or note text.
	if ownerID != authorID {
		payload, _ := json.Marshal(map[string]any{"annotationId": annotationID, "targetType": targetType, "targetId": targetID})
		if _, err := RecordUserEvent(ctx, tx, UserEventInput{
			RecipientID: ownerID, ActorID: authorID, Kind: "annotation_reply",
			ResourceType: "annotation", ResourceID: annotationID,
			IdempotencyKey: "annreply:" + reply.ID, Payload: payload,
		}); err != nil {
			return AnnotationReply{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AnnotationReply{}, err
	}
	return reply, nil
}

// ListReplies returns replies for an annotation in ascending order.
func ListReplies(ctx context.Context, annotationID string, limit int) ([]AnnotationReply, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := dbPool.Query(ctx, `
		SELECT id::text, annotation_id::text, user_id::text, body, created_at
		FROM annotation_replies WHERE annotation_id = $1
		ORDER BY created_at ASC, id ASC LIMIT $2
	`, annotationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnnotationReply{}
	for rows.Next() {
		var r AnnotationReply
		if err := rows.Scan(&r.ID, &r.AnnotationID, &r.AuthorID, &r.Body, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteReply removes a reply; the author may delete their own, a moderator any.
func DeleteReply(ctx context.Context, replyID string, actor *UserContext) error {
	var authorID string
	err := dbPool.QueryRow(ctx, `SELECT user_id::text FROM annotation_replies WHERE id = $1`, replyID).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errAnnotationNotFound
	}
	if err != nil {
		return err
	}
	if authorID != actor.ID {
		can, perr := hasPermission(ctx, actor, "moderate_annotations", nil)
		if perr != nil {
			return perr
		}
		if !can {
			return errAnnotationDenied
		}
	}
	_, err = dbPool.Exec(ctx, `DELETE FROM annotation_replies WHERE id = $1`, replyID)
	return err
}
