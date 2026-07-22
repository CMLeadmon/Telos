package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// handleListNotifications serves the recipient's inbox as a stable descending
// (created_at, id) cursor page.
func handleListNotifications(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	req, _, err := resolvePageRequest("notifications", r.URL.Query().Get("sort"),
		r.URL.Query().Get("cursor"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The list request is invalid.")
		return
	}
	var seekTime time.Time
	var seekID string
	haveSeek := false
	if req.After != "" {
		c, derr := cursorCodec.Decode("notifications", req.After)
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
	items, err := ListNotifications(r.Context(), user.ID, seekTime, seekID, haveSeek, req.Limit+1)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	page := Page[Notification]{Items: items}
	if len(items) > req.Limit {
		page.Items = items[:req.Limit]
		last := page.Items[req.Limit-1]
		next, encErr := cursorCodec.Encode("notifications", PageCursor{
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

func handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	n, err := UnreadCount(r.Context(), user.ID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"count": n})
}

func handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id := r.PathValue("id")
	if err := MarkRead(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, errNotificationNotFound) {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	n, err := MarkAllRead(r.Context(), user.ID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"updated": n})
}

// handleEventsCatchUp serves the recipient's forward catch-up window.
func handleEventsCatchUp(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	after, aerr := parseSequenceParam(r.URL.Query().Get("afterSequence"))
	through, terr := parseSequenceParam(r.URL.Query().Get("throughSequence"))
	if aerr != nil || terr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Sequence bounds must be non-negative integers.")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), maxUserEventPage)
	res, err := CatchUp(r.Context(), user.ID, after, through, limit)
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

func parseSequenceParam(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidSequenceBounds
	}
	return n, nil
}

// handleEventsWS is the authenticated, bounded, revocable user-event socket. It
// subscribes only to the recipient's own telos:user:<id> channel; it never
// exposes another user's events.
func handleEventsWS(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	userID := user.ID

	rawToken, tokErr := sessionTokenFromRequest(r)
	if tokErr != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	sessionHash := sha256Hex(rawToken)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("events WS upgrade failed request_id=%s: %v", requestIDFrom(r.Context()), err)
		return
	}
	client := newWSClient(conn)
	defer client.close()

	release, ok := sessionRegistryInstance.Register(sessionHash, userID, client)
	if !ok {
		client.CloseWithCode(websocket.ClosePolicyViolation, "too many connections")
		return
	}
	defer release()

	conn.SetReadLimit(wsInboundLimitBytes)
	const pongWait = 60 * time.Second
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(pongWait)) })

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var wg sync.WaitGroup

	// Optional catch-up on connect closes any gap since the client's last seen
	// sequence exactly once, before live delivery begins.
	if after, perr := parseSequenceParam(r.URL.Query().Get("afterSequence")); perr == nil && r.URL.Query().Get("afterSequence") != "" {
		if cu, err := CatchUp(ctx, userID, after, 0, maxUserEventPage); err == nil {
			client.sendJSON(map[string]any{"type": "catchup", "events": cu.Items, "highWater": cu.HighWater})
		}
	}

	pubsub := redisClient.Subscribe(ctx, "telos:user:"+userID)
	defer pubsub.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ch := pubsub.Channel()
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if !client.sendText([]byte(msg.Payload)) {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	revalidate := time.NewTicker(30 * time.Second)
	defer revalidate.Stop()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ticker.C:
				if err := client.ping(); err != nil {
					cancel()
					client.close()
					return
				}
			case <-revalidate.C:
				if !sessionStillValid(ctx, sessionHash) {
					client.CloseWithCode(websocket.ClosePolicyViolation, "session no longer valid")
					cancel()
					return
				}
			case <-ctx.Done():
				return
			case <-client.done:
				cancel()
				return
			}
		}
	}()

	log.Printf("events client connected request_id=%s", requestIDFrom(r.Context()))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	cancel()
	wg.Wait()
}

// sessionStillValid reports whether the session behind sessionHash is still
// active (not revoked or expired), so a stale socket is closed on the fallback.
func sessionStillValid(ctx context.Context, sessionHash string) bool {
	var valid bool
	err := dbPool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sessions
			WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		)
	`, sessionHash).Scan(&valid)
	if err != nil {
		return false
	}
	return valid
}
