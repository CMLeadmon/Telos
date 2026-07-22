package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotificationKind is a constrained in-app notification category.
type NotificationKind string

const (
	NotifyMention          NotificationKind = "mention"
	NotifyThreadReply      NotificationKind = "thread_reply"
	NotifyAnnotationReply  NotificationKind = "annotation_reply"
	NotifyWatchPartyInvite NotificationKind = "watch_party_invite"
	NotifyAccountSecurity  NotificationKind = "account_security"
)

// NotificationInput is a request to materialize a notification and its matching
// durable user event.
type NotificationInput struct {
	RecipientID    string
	ActorID        string
	Kind           NotificationKind
	ResourceType   string
	ResourceID     string
	IdempotencyKey string
	Payload        json.RawMessage
}

// Notification is one inbox row.
type Notification struct {
	ID            string           `json:"id"`
	RecipientID   string           `json:"-"`
	ActorID       string           `json:"actorId,omitempty"`
	Kind          NotificationKind `json:"kind"`
	ResourceType  string           `json:"resourceType"`
	ResourceID    string           `json:"resourceId"`
	Payload       json.RawMessage  `json:"payload"`
	EventSequence int64            `json:"sequence"`
	Read          bool             `json:"read"`
	CreatedAt     time.Time        `json:"createdAt"`
}

var (
	errNotificationInput    = errors.New("notification input is incomplete")
	errNotificationNotFound = errors.New("notification not found")
)

// CreateNotification writes a notification, its matching durable user event, and
// a telos:user:<recipient> outbox delivery hint inside the caller's
// transaction. It is idempotent on IdempotencyKey: a replay returns the existing
// notification and inserts no second event, notification, or outbox row.
func CreateNotification(ctx context.Context, tx pgx.Tx, input NotificationInput) (Notification, error) {
	if input.RecipientID == "" || input.Kind == "" || input.IdempotencyKey == "" {
		return Notification{}, errNotificationInput
	}
	payload := input.Payload
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage("{}")
	}
	// A non-UUID actor (e.g. a system actor) is stored as NULL rather than
	// failing the whole security operation on a cast error.
	actorID := input.ActorID
	if !looksLikeUUID(actorID) {
		actorID = ""
	}

	// Claim the idempotency key first, without an event, so a concurrent replay
	// that loses the race never inserts a duplicate user_event.
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO notifications (user_id, actor_id, kind, resource_type, resource_id, idempotency_key, payload)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id::text
	`, input.RecipientID, actorID, string(input.Kind), input.ResourceType, input.ResourceID, input.IdempotencyKey, payload).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// A notification with this key already exists: return it unchanged.
		return loadNotificationByKey(ctx, tx, input.IdempotencyKey)
	}
	if err != nil {
		return Notification{}, err
	}

	// We own this notification; append its durable event and link it.
	seq, err := insertUserEvent(ctx, tx, input.RecipientID, string(input.Kind), input.ResourceType, input.ResourceID, payload)
	if err != nil {
		return Notification{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET event_sequence = $2 WHERE id = $1`, id, seq); err != nil {
		return Notification{}, err
	}

	// Delivery hint on the recipient's channel (deduped by the outbox itself).
	hint, _ := json.Marshal(map[string]any{"sequence": seq, "kind": string(input.Kind)})
	if _, err := EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic:          "telos:user:" + input.RecipientID,
		EventType:      "notification",
		AggregateType:  "notification",
		AggregateID:    id,
		IdempotencyKey: "notif:" + id,
		Payload:        hint,
	}); err != nil {
		return Notification{}, err
	}

	return Notification{
		ID: id, RecipientID: input.RecipientID, ActorID: actorID, Kind: input.Kind,
		ResourceType: input.ResourceType, ResourceID: input.ResourceID, Payload: payload,
		EventSequence: seq, Read: false, CreatedAt: time.Now(),
	}, nil
}

func loadNotificationByKey(ctx context.Context, q pgxQuerier, key string) (Notification, error) {
	var n Notification
	var payload []byte
	var seq *int64
	err := q.QueryRow(ctx, `
		SELECT id::text, COALESCE(actor_id::text,''), kind, resource_type, resource_id, payload, event_sequence, read_at IS NOT NULL, created_at
		FROM notifications WHERE idempotency_key = $1
	`, key).Scan(&n.ID, &n.ActorID, &n.Kind, &n.ResourceType, &n.ResourceID, &payload, &seq, &n.Read, &n.CreatedAt)
	if err != nil {
		return Notification{}, err
	}
	n.Payload = json.RawMessage(payload)
	if seq != nil {
		n.EventSequence = *seq
	}
	return n, nil
}

// looksLikeUUID reports whether s has canonical 8-4-4-4-12 hex UUID form.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

// pgxQuerier is the read surface shared by *pgxpool.Pool and pgx.Tx.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ListNotifications returns a descending (created_at, id) page for a recipient.
func ListNotifications(ctx context.Context, recipientID string, seekTime time.Time, seekID string, haveSeek bool, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := dbPool.Query(ctx, `
		SELECT id::text, COALESCE(actor_id::text,''), kind, resource_type, resource_id, payload,
		       COALESCE(event_sequence,0), read_at IS NOT NULL, created_at
		FROM notifications
		WHERE user_id = $1
		  AND (($2::boolean IS FALSE) OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, recipientID, haveSeek, seekTime, seekIDArg(seekID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var payload []byte
		if err := rows.Scan(&n.ID, &n.ActorID, &n.Kind, &n.ResourceType, &n.ResourceID, &payload, &n.EventSequence, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Payload = json.RawMessage(payload)
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount returns the recipient's unread notification count.
func UnreadCount(ctx context.Context, recipientID string) (int, error) {
	var n int
	err := dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, recipientID).Scan(&n)
	return n, err
}

// MarkRead marks one notification read for its recipient. It is idempotent and
// scoped: another user's notification is never touched and yields not-found.
func MarkRead(ctx context.Context, recipientID, notificationID string) error {
	tag, err := dbPool.Exec(ctx, `
		UPDATE notifications SET read_at = COALESCE(read_at, now())
		WHERE id = $1 AND user_id = $2
	`, notificationID, recipientID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotificationNotFound
	}
	return nil
}

// MarkAllRead marks every unread notification read for a recipient.
func MarkAllRead(ctx context.Context, recipientID string) (int64, error) {
	tag, err := dbPool.Exec(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL
	`, recipientID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
