package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// decodeSeekCursor decodes a (time, uuid) seek cursor for a scope.
func decodeSeekCursor(scope, token string) (seekTime time.Time, seekID string, haveSeek bool, ok bool) {
	if token == "" {
		return time.Time{}, "", false, true
	}
	c, err := cursorCodec.Decode(scope, token)
	if err != nil || len(c.Values) != 2 {
		return time.Time{}, "", false, false
	}
	t, terr := time.Parse(time.RFC3339Nano, c.Values[0].Value)
	if terr != nil {
		return time.Time{}, "", false, false
	}
	return t, c.Values[1].Value, true, true
}

func encodeSeekCursor(scope, sort string, last ChatMessage) string {
	tok, err := cursorCodec.Encode(scope, PageCursor{
		Sort:     sort,
		Values:   []CursorValue{{Kind: "time", Value: last.CreatedAt.Format(time.RFC3339Nano)}, {Kind: "uuid", Value: last.ID}},
		ID:       last.ID,
		IssuedAt: time.Now(),
	})
	if err != nil {
		return ""
	}
	return tok
}

// handleListChannelRoots serves root-only channel history (descending).
func handleListChannelRoots(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	req, _, err := resolvePageRequest("channel.roots", r.URL.Query().Get("sort"), r.URL.Query().Get("cursor"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The list request is invalid.")
		return
	}
	seekTime, seekID, haveSeek, valid := decodeSeekCursor("channel.roots", req.After)
	if !valid {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid.")
		return
	}
	items, err := ListChannelRoots(r.Context(), channelID, seekTime, seekID, haveSeek, req.Limit+1)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	writeThreadPage(w, "channel.roots", req, items)
}

// handleListThreadReplies serves ascending replies under a root.
func handleListThreadReplies(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	rootID := r.PathValue("rootID")
	if !looksLikeUUID(rootID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid thread root.")
		return
	}
	req, _, err := resolvePageRequest("channel.replies", r.URL.Query().Get("sort"), r.URL.Query().Get("cursor"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The list request is invalid.")
		return
	}
	seekTime, seekID, haveSeek, valid := decodeSeekCursor("channel.replies", req.After)
	if !valid {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid.")
		return
	}
	items, err := ListThreadReplies(r.Context(), channelID, rootID, seekTime, seekID, haveSeek, req.Limit+1)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	writeThreadPage(w, "channel.replies", req, items)
}

func writeThreadPage(w http.ResponseWriter, scope string, req PageRequest, items []ChatMessage) {
	page := Page[ChatMessage]{Items: items}
	if len(items) > req.Limit {
		page.Items = items[:req.Limit]
		if next := encodeSeekCursor(scope, req.Sort, page.Items[req.Limit-1]); next != "" {
			page.NextCursor = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// handleChannelChanges serves the forward change catch-up for a channel. An
// expired cursor returns 410 with the authorized current high-water.
func handleChannelChanges(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	after, aerr := parseSequenceParam(r.URL.Query().Get("afterSequence"))
	through, terr := parseSequenceParam(r.URL.Query().Get("throughSequence"))
	if aerr != nil || terr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Sequence bounds must be non-negative integers.")
		return
	}
	res, err := CatchUpChannelChanges(r.Context(), channelID, after, through, atoiDefault(r.URL.Query().Get("limit"), maxUserEventPage))
	if errors.Is(err, errChangeCursorExpired) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]any{"error": "change_cursor_expired", "highWater": res.HighWater})
		return
	}
	if err != nil {
		if errors.Is(err, errInvalidSequenceBounds) {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Sequence bounds must be non-negative integers.")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// handleMarkChannelRead advances the viewer's read marker monotonically to a
// message in that channel.
func handleMarkChannelRead(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		MessageID string `json:"messageId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !looksLikeUUID(body.MessageID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid message id is required.")
		return
	}
	// The message must belong to this channel.
	var msgTime time.Time
	err := dbPool.QueryRow(r.Context(), `SELECT created_at FROM messages WHERE id = $1 AND channel_id = $2`, body.MessageID, channelID).Scan(&msgTime)
	if errors.Is(err, pgx.ErrNoRows) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	// Advance only forward: never move the marker to an older message.
	_, err = dbPool.Exec(r.Context(), `
		INSERT INTO channel_reads (user_id, channel_id, last_read_message_id, last_read_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, channel_id) DO UPDATE
		SET last_read_message_id = EXCLUDED.last_read_message_id, last_read_at = now()
		WHERE channel_reads.last_read_at < EXCLUDED.last_read_at
		  AND COALESCE((SELECT created_at FROM messages WHERE id = channel_reads.last_read_message_id), 'epoch'::timestamptz) <= $4
	`, user.ID, channelID, body.MessageID, msgTime)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// MessageActor is a compact author reference for thread metadata.
type MessageActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// ChatMessage is a thread-aware message row for root/reply listings.
type ChatMessage struct {
	ID               string        `json:"id"`
	ChannelID        string        `json:"channelId"`
	AuthorID         string        `json:"authorId"`
	Author           string        `json:"author"`
	Content          string        `json:"content"`
	ThreadRootID     *string       `json:"threadRootId,omitempty"`
	ClientMutationID string        `json:"clientMutationId,omitempty"`
	ReplyCount       int           `json:"replyCount"`
	LastReplyAt      *time.Time    `json:"lastReplyAt,omitempty"`
	LastReplyBy      *MessageActor `json:"lastReplyBy,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
}

var (
	errReplyToReply      = errors.New("cannot reply to a reply")
	errThreadRootMissing = errors.New("thread root not found in channel")
)

// mentionPattern matches @username tokens (letters, digits, underscore, hyphen).
var mentionPattern = regexp.MustCompile(`@([A-Za-z0-9_-]{1,32})`)

// createChatMessageTx inserts a message (root or reply) idempotently on the
// author's client mutation id, appends a channel-change row, and materializes
// mention and thread-reply notifications — all inside the caller's transaction.
// A retry by the same author with the same clientMutationID returns the original
// message and creates no duplicate change or notification.
type messageEmbedInput struct {
	Kind     *string
	Ref      *string
	Snapshot []byte
}

func createChatMessageTx(ctx context.Context, tx pgx.Tx, channelID, authorID, content, clientMutationID string, threadRootID *string, embed messageEmbedInput) (id string, ts time.Time, createdNew bool, err error) {
	if clientMutationID != "" {
		// Return the original message on a duplicate mutation.
		var existingID string
		var existingTS time.Time
		e := tx.QueryRow(ctx, `
			SELECT id::text, created_at FROM messages
			WHERE user_id = $1 AND client_mutation_id = $2
		`, authorID, clientMutationID).Scan(&existingID, &existingTS)
		if e == nil {
			return existingID, existingTS, false, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return "", time.Time{}, false, e
		}
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO messages (channel_id, user_id, content, thread_root_id, client_mutation_id, embed_kind, embed_ref, embed_snapshot)
		VALUES ($1::uuid, $2::uuid, $3, NULLIF($4,'')::uuid, NULLIF($5,''), $6, $7, $8)
		RETURNING id::text, created_at
	`, channelID, authorID, content, ptrToStr(threadRootID), clientMutationID, embed.Kind, embed.Ref, embed.Snapshot).Scan(&id, &ts)
	if err != nil {
		// The thread-root enforcement trigger raises on a reply-to-reply or a
		// cross-channel/missing root.
		if strings.Contains(err.Error(), "thread root must be a root") {
			return "", time.Time{}, false, errThreadRootMissing
		}
		if strings.Contains(err.Error(), "own thread root") {
			return "", time.Time{}, false, errReplyToReply
		}
		// A concurrent duplicate mutation insert loses the unique race; return
		// the winner's row.
		if isUniqueViolation(err) && clientMutationID != "" {
			var existingID string
			var existingTS time.Time
			if e := tx.QueryRow(ctx, `SELECT id::text, created_at FROM messages WHERE user_id=$1 AND client_mutation_id=$2`, authorID, clientMutationID).Scan(&existingID, &existingTS); e == nil {
				return existingID, existingTS, false, nil
			}
		}
		return "", time.Time{}, false, err
	}

	if err = appendChannelChangeTx(ctx, tx, channelID, "message.created", id); err != nil {
		return "", time.Time{}, false, err
	}
	if err = notifyForMessageTx(ctx, tx, channelID, authorID, id, content, threadRootID); err != nil {
		return "", time.Time{}, false, err
	}
	return id, ts, true, nil
}

// appendChannelChangeTx records a durable, strictly-increasing channel change.
func appendChannelChangeTx(ctx context.Context, tx pgx.Tx, channelID, kind, messageID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_changes (channel_id, kind, message_id)
		VALUES ($1::uuid, $2, NULLIF($3,'')::uuid)
	`, channelID, kind, messageID)
	return err
}

// notifyForMessageTx creates thread-reply and @mention notifications for a new
// message. Notifications are idempotent by key and never notify the author of
// their own message.
func notifyForMessageTx(ctx context.Context, tx pgx.Tx, channelID, authorID, messageID, content string, threadRootID *string) error {
	notified := map[string]struct{}{authorID: {}}

	// Thread reply: notify the root author.
	if threadRootID != nil && *threadRootID != "" {
		var rootAuthor string
		err := tx.QueryRow(ctx, `SELECT user_id::text FROM messages WHERE id = $1`, *threadRootID).Scan(&rootAuthor)
		if err == nil && rootAuthor != "" {
			if _, dup := notified[rootAuthor]; !dup {
				notified[rootAuthor] = struct{}{}
				payload, _ := json.Marshal(map[string]string{"channelId": channelID, "rootId": *threadRootID})
				if _, err := CreateNotification(ctx, tx, NotificationInput{
					RecipientID: rootAuthor, ActorID: authorID, Kind: NotifyThreadReply,
					ResourceType: "message", ResourceID: messageID,
					IdempotencyKey: "threadreply:" + messageID + ":" + rootAuthor, Payload: payload,
				}); err != nil {
					return err
				}
			}
		}
	}

	// @mentions: resolve usernames to members and notify each once.
	usernames := map[string]struct{}{}
	for _, m := range mentionPattern.FindAllStringSubmatch(content, -1) {
		usernames[strings.ToLower(m[1])] = struct{}{}
	}
	for uname := range usernames {
		var uid string
		err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(username) = $1 AND active`, uname).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, dup := notified[uid]; dup {
			continue
		}
		notified[uid] = struct{}{}
		payload, _ := json.Marshal(map[string]string{"channelId": channelID})
		if _, err := CreateNotification(ctx, tx, NotificationInput{
			RecipientID: uid, ActorID: authorID, Kind: NotifyMention,
			ResourceType: "message", ResourceID: messageID,
			IdempotencyKey: "mention:" + messageID + ":" + uid, Payload: payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListChannelRoots returns root messages (no thread parent) for a channel in
// descending (created_at, id) order with reply metadata, without an N+1 lookup.
func ListChannelRoots(ctx context.Context, channelID string, seekTime time.Time, seekID string, haveSeek bool, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := dbPool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, m.user_id::text, COALESCE(u.display_name, u.username),
		       m.content, m.created_at,
		       COALESCE(r.reply_count,0), r.last_reply_at, r.last_reply_by::text, COALESCE(ru.display_name, ru.username, '')
		FROM messages m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN LATERAL (
		    SELECT COUNT(*) AS reply_count, MAX(created_at) AS last_reply_at,
		           (SELECT user_id FROM messages x WHERE x.thread_root_id = m.id ORDER BY created_at DESC, id DESC LIMIT 1) AS last_reply_by
		    FROM messages rep WHERE rep.thread_root_id = m.id
		) r ON TRUE
		LEFT JOIN users ru ON ru.id = r.last_reply_by
		WHERE m.channel_id = $1 AND m.thread_root_id IS NULL AND m.deleted_at IS NULL
		  AND (($2::boolean IS FALSE) OR (m.created_at, m.id) < ($3::timestamptz, $4::uuid))
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $5
	`, channelID, haveSeek, seekTime, seekIDArg(seekID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreadRows(rows)
}

// ListThreadReplies returns replies under a root in ascending order.
func ListThreadReplies(ctx context.Context, channelID, rootID string, seekTime time.Time, seekID string, haveSeek bool, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := dbPool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, m.user_id::text, COALESCE(u.display_name, u.username),
		       m.content, m.created_at, 0, NULL::timestamptz, NULL::text, ''
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.channel_id = $1 AND m.thread_root_id = $2 AND m.deleted_at IS NULL
		  AND (($3::boolean IS FALSE) OR (m.created_at, m.id) > ($4::timestamptz, $5::uuid))
		ORDER BY m.created_at ASC, m.id ASC
		LIMIT $6
	`, channelID, rootID, haveSeek, seekTime, seekIDArg(seekID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreadRows(rows)
}

func scanThreadRows(rows pgx.Rows) ([]ChatMessage, error) {
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		var lastReplyAt *time.Time
		var lastReplyBy *string
		var lastReplyName string
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Author, &m.Content, &m.CreatedAt,
			&m.ReplyCount, &lastReplyAt, &lastReplyBy, &lastReplyName); err != nil {
			return nil, err
		}
		m.LastReplyAt = lastReplyAt
		if lastReplyBy != nil && *lastReplyBy != "" {
			m.LastReplyBy = &MessageActor{ID: *lastReplyBy, DisplayName: lastReplyName}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func ptrToStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ChannelChange is one compact change-log entry.
type ChannelChange struct {
	Sequence   int64     `json:"sequence"`
	Kind       string    `json:"kind"`
	MessageID  string    `json:"messageId,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
}

// ChannelChangeCatchUp is a bounded ascending change page with the retained
// high-water mark.
type ChannelChangeCatchUp struct {
	Items     []ChannelChange `json:"items"`
	NextAfter ChangeCursor    `json:"nextAfter"`
	HighWater ChangeCursor    `json:"highWater"`
	HasMore   bool            `json:"hasMore"`
}

// channelChangeRetention bounds the durable change log; older rows are pruned.
const channelChangeRetention = 7 * 24 * time.Hour

var errChangeCursorExpired = errors.New("change cursor expired")

// CatchUpChannelChanges returns afterSequence < seq <= throughSequence for a
// channel in ascending order. A zero throughSequence samples the current
// maximum once. A cursor older than the retained floor returns
// errChangeCursorExpired plus the current high-water so the client full-resyncs.
func CatchUpChannelChanges(ctx context.Context, channelID string, afterSequence, throughSequence int64, limit int) (ChannelChangeCatchUp, error) {
	if afterSequence < 0 || throughSequence < 0 {
		return ChannelChangeCatchUp{}, errInvalidSequenceBounds
	}
	if limit <= 0 || limit > maxUserEventPage {
		limit = maxUserEventPage
	}

	// Current high-water and retained floor for the channel.
	var maxSeq *int64
	var floorSeq *int64
	if err := dbPool.QueryRow(ctx, `
		SELECT MAX(sequence),
		       MIN(sequence) FILTER (WHERE occurred_at >= now() - $2::interval)
		FROM channel_changes WHERE channel_id = $1
	`, channelID, channelChangeRetention.String()).Scan(&maxSeq, &floorSeq); err != nil {
		return ChannelChangeCatchUp{}, err
	}
	high := throughSequence
	if high == 0 && maxSeq != nil {
		high = *maxSeq
	}

	// A nonzero afterSequence below the retained floor cannot be served
	// completely; signal a full resync with the authorized current high-water.
	if afterSequence > 0 && floorSeq != nil && afterSequence < *floorSeq-1 {
		return ChannelChangeCatchUp{HighWater: ChangeCursor(high), NextAfter: ChangeCursor(afterSequence)}, errChangeCursorExpired
	}

	rows, err := dbPool.Query(ctx, `
		SELECT sequence, kind, COALESCE(message_id::text,''), occurred_at
		FROM channel_changes
		WHERE channel_id = $1 AND sequence > $2 AND sequence <= $3
		ORDER BY sequence ASC
		LIMIT $4
	`, channelID, afterSequence, high, limit+1)
	if err != nil {
		return ChannelChangeCatchUp{}, err
	}
	defer rows.Close()
	items := []ChannelChange{}
	for rows.Next() {
		var c ChannelChange
		if err := rows.Scan(&c.Sequence, &c.Kind, &c.MessageID, &c.OccurredAt); err != nil {
			return ChannelChangeCatchUp{}, err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return ChannelChangeCatchUp{}, err
	}
	out := ChannelChangeCatchUp{HighWater: ChangeCursor(high), NextAfter: ChangeCursor(afterSequence)}
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

// PruneChannelChanges deletes change rows older than the retention window in one
// bounded batch, returning the number removed.
func PruneChannelChanges(ctx context.Context) (int64, error) {
	tag, err := dbPool.Exec(ctx, `
		DELETE FROM channel_changes
		WHERE ctid IN (
		    SELECT ctid FROM channel_changes
		    WHERE occurred_at < now() - $1::interval
		    LIMIT 1000
		)
	`, channelChangeRetention.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
