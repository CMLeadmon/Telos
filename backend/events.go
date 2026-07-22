package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChangeCursor is an opaque, per-recipient monotonic position in the user-event
// stream. It is just a sequence value; because the stream is recipient-scoped, a
// recipient only ever sees its own positions.
type ChangeCursor int64

// UserEvent is one durable, recipient-scoped change record.
type UserEvent struct {
	Sequence     int64           `json:"sequence"`
	Kind         string          `json:"kind"`
	ResourceType string          `json:"resourceType"`
	ResourceID   string          `json:"resourceId"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// UserEventCatchUp is a bounded ascending page plus the retained high-water mark
// so a reconnecting socket can close a gap deterministically.
type UserEventCatchUp struct {
	Items     []UserEvent  `json:"items"`
	NextAfter ChangeCursor `json:"nextAfter"`
	HighWater ChangeCursor `json:"highWater"`
	HasMore   bool         `json:"hasMore"`
}

const maxUserEventPage = 100

var errInvalidSequenceBounds = errors.New("invalid sequence bounds")

// insertUserEvent appends a strictly-increasing event for a recipient inside the
// caller's transaction and returns its sequence.
func insertUserEvent(ctx context.Context, tx pgx.Tx, recipientID, kind, resourceType, resourceID string, payload json.RawMessage) (int64, error) {
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage("{}")
	}
	var seq int64
	err := tx.QueryRow(ctx, `
		INSERT INTO user_events (recipient_id, kind, resource_type, resource_id, payload)
		VALUES ($1::uuid, $2, $3, $4, $5)
		RETURNING sequence
	`, recipientID, kind, resourceType, resourceID, payload).Scan(&seq)
	return seq, err
}

// CatchUp returns the recipient's events with afterSequence < sequence <=
// throughSequence in ascending order, bounded by limit. A zero throughSequence
// samples the recipient's current maximum once and returns it as HighWater, so
// later pages of the same catch-up can pass it back to keep a stable ceiling.
func CatchUp(ctx context.Context, recipientID string, afterSequence, throughSequence int64, limit int) (UserEventCatchUp, error) {
	if afterSequence < 0 || throughSequence < 0 {
		return UserEventCatchUp{}, errInvalidSequenceBounds
	}
	if limit <= 0 || limit > maxUserEventPage {
		limit = maxUserEventPage
	}

	high := throughSequence
	if high == 0 {
		// Sample the recipient's current maximum sequence exactly once.
		var maxSeq *int64
		if err := dbPool.QueryRow(ctx, `SELECT MAX(sequence) FROM user_events WHERE recipient_id = $1`, recipientID).Scan(&maxSeq); err != nil {
			return UserEventCatchUp{}, err
		}
		if maxSeq != nil {
			high = *maxSeq
		}
	}
	if afterSequence > high {
		// Nothing to catch up on; report the stable ceiling.
		return UserEventCatchUp{Items: []UserEvent{}, NextAfter: ChangeCursor(afterSequence), HighWater: ChangeCursor(high)}, nil
	}

	rows, err := dbPool.Query(ctx, `
		SELECT sequence, kind, resource_type, resource_id, payload, created_at
		FROM user_events
		WHERE recipient_id = $1 AND sequence > $2 AND sequence <= $3
		ORDER BY sequence ASC
		LIMIT $4
	`, recipientID, afterSequence, high, limit+1)
	if err != nil {
		return UserEventCatchUp{}, err
	}
	defer rows.Close()

	items := []UserEvent{}
	for rows.Next() {
		var e UserEvent
		var payload []byte
		if err := rows.Scan(&e.Sequence, &e.Kind, &e.ResourceType, &e.ResourceID, &payload, &e.CreatedAt); err != nil {
			return UserEventCatchUp{}, err
		}
		e.Payload = json.RawMessage(payload)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return UserEventCatchUp{}, err
	}

	out := UserEventCatchUp{HighWater: ChangeCursor(high), NextAfter: ChangeCursor(afterSequence)}
	if len(items) > limit {
		items = items[:limit]
		out.HasMore = true
	}
	out.Items = items
	if len(items) > 0 {
		out.NextAfter = ChangeCursor(items[len(items)-1].Sequence)
	}
	return out, nil
}
