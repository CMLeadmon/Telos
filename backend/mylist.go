package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

func atoi64(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
func i64toa(n int64) string { return strconv.FormatInt(n, 10) }

// MediaListEntry is one My List item with its non-authoritative display
// snapshot and current upstream availability.
type MediaListEntry struct {
	ItemID    string `json:"itemId"`
	Title     string `json:"title"`
	MediaType string `json:"mediaType"`
	Position  int64  `json:"position"`
	Available bool   `json:"available"`
}

var (
	errListChanged           = errors.New("list changed")
	errListStaleRevision     = errors.New("stale revision")
	errListItemNotAuthorized = errors.New("item not authorized")
	errListItemMissing       = errors.New("item not in list")
)

// lockList creates (if absent) and row-locks the user's list, returning the
// current revision. Every add/remove/reorder runs under this lock.
func lockList(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	var rev int64
	err := tx.QueryRow(ctx, `
		INSERT INTO media_lists (user_id) VALUES ($1::uuid)
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING revision
	`, userID).Scan(&rev)
	return rev, err
}

// AddToList appends an authorized item to the end of the list. A duplicate add
// is an idempotent no-op that does not bump the revision.
func AddToList(ctx context.Context, userID string, item AuthorizedMediaItem) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockList(ctx, tx, userID); err != nil {
		return err
	}
	var pos int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM media_list_entries WHERE user_id = $1`, userID).Scan(&pos); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO media_list_entries (user_id, item_id, position, title, media_type)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (user_id, item_id) DO NOTHING
	`, userID, item.ID, pos, item.Name, item.MediaType)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `UPDATE media_lists SET revision = revision + 1 WHERE user_id = $1`, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RemoveFromList removes an item, bumping the revision only if a row was removed.
func RemoveFromList(ctx context.Context, userID, itemID string) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockList(ctx, tx, userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM media_list_entries WHERE user_id = $1 AND item_id = $2`, userID, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `UPDATE media_lists SET revision = revision + 1 WHERE user_id = $1`, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ReorderList moves itemID immediately before beforeItemID (or to the end when
// nil), under the list lock, rejecting a stale expectedRevision. Positions are
// rewritten to a unique contiguous 1..N sequence.
func ReorderList(ctx context.Context, userID, itemID string, beforeItemID *string, expectedRevision int64) (int64, error) {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rev, err := lockList(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if rev != expectedRevision {
		return rev, errListStaleRevision
	}

	rows, err := tx.Query(ctx, `SELECT item_id FROM media_list_entries WHERE user_id = $1 ORDER BY position ASC`, userID)
	if err != nil {
		return 0, err
	}
	var order []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		order = append(order, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	newOrder, ok := moveBefore(order, itemID, beforeItemID)
	if !ok {
		return 0, errListItemMissing
	}

	// Shift out of the way to avoid unique collisions, then assign 1..N.
	if _, err := tx.Exec(ctx, `UPDATE media_list_entries SET position = position + 1000000000 WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	for i, id := range newOrder {
		if _, err := tx.Exec(ctx, `UPDATE media_list_entries SET position = $2 WHERE user_id = $1 AND item_id = $3`, userID, int64(i+1), id); err != nil {
			return 0, err
		}
	}
	var newRev int64
	if err := tx.QueryRow(ctx, `UPDATE media_lists SET revision = revision + 1 WHERE user_id = $1 RETURNING revision`, userID).Scan(&newRev); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newRev, nil
}

// moveBefore returns order with itemID relocated immediately before beforeItemID
// (or appended when beforeItemID is nil), or ok=false if itemID is absent.
func moveBefore(order []string, itemID string, beforeItemID *string) ([]string, bool) {
	out := make([]string, 0, len(order))
	found := false
	for _, id := range order {
		if id == itemID {
			found = true
			continue
		}
		out = append(out, id)
	}
	if !found {
		return nil, false
	}
	if beforeItemID == nil || *beforeItemID == "" {
		return append(out, itemID), true
	}
	res := make([]string, 0, len(order))
	inserted := false
	for _, id := range out {
		if id == *beforeItemID {
			res = append(res, itemID)
			inserted = true
		}
		res = append(res, id)
	}
	if !inserted {
		res = append(res, itemID) // target gone: append
	}
	return res, true
}

// ListMyList returns a page of entries starting after a position, along with the
// current list revision. A cursor whose revision no longer matches returns
// errListChanged so the client reloads from page one.
func ListMyList(ctx context.Context, userID string, afterPosition, cursorRevision int64, haveCursor bool, limit int) ([]MediaListEntry, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rev int64
	err := dbPool.QueryRow(ctx, `SELECT COALESCE((SELECT revision FROM media_lists WHERE user_id = $1), 0)`, userID).Scan(&rev)
	if err != nil {
		return nil, 0, err
	}
	if haveCursor && cursorRevision != rev {
		return nil, rev, errListChanged
	}
	rows, err := dbPool.Query(ctx, `
		SELECT item_id, title, media_type, position
		FROM media_list_entries
		WHERE user_id = $1 AND (($2::boolean IS FALSE) OR position > $3)
		ORDER BY position ASC
		LIMIT $4
	`, userID, haveCursor, afterPosition, limit)
	if err != nil {
		return nil, rev, err
	}
	defer rows.Close()
	out := []MediaListEntry{}
	for rows.Next() {
		var e MediaListEntry
		if err := rows.Scan(&e.ItemID, &e.Title, &e.MediaType, &e.Position); err != nil {
			return nil, rev, err
		}
		e.Available = true // refined by the caller's batch authorization
		out = append(out, e)
	}
	return out, rev, rows.Err()
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

// MediaListPage is a page plus the list revision (for cursor binding).
type MediaListPage struct {
	Items        []MediaListEntry `json:"items"`
	NextCursor   string           `json:"nextCursor,omitempty"`
	ListRevision int64            `json:"listRevision"`
}

func handleGetMyList(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	req, _, err := resolvePageRequest("media.list", r.URL.Query().Get("sort"), r.URL.Query().Get("cursor"), atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The list request is invalid.")
		return
	}
	var afterPos, curRev int64
	haveCursor := false
	if req.After != "" {
		c, derr := cursorCodec.Decode("media.list", req.After)
		if derr != nil || len(c.Values) != 2 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid.")
			return
		}
		curRev = atoi64(c.Values[0].Value)
		afterPos = atoi64(c.Values[1].Value)
		haveCursor = true
	}
	entries, rev, err := ListMyList(r.Context(), user.ID, afterPos, curRev, haveCursor, req.Limit+1)
	if errors.Is(err, errListChanged) {
		writeAPIError(w, r, http.StatusConflict, "list_changed", "Your list changed; reload from the start.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	// One bounded batch authorization per page; missing/unauthorized items keep
	// their identity but are marked unavailable rather than failing the page.
	if jellyfinAuthorizer != nil && len(entries) > 0 {
		ids := make([]string, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.ItemID)
		}
		authorized, aerr := jellyfinAuthorizer.AuthorizeItems(r.Context(), ids)
		if aerr == nil {
			for i := range entries {
				_, ok := authorized[entries[i].ItemID]
				entries[i].Available = ok
			}
		}
	}

	page := MediaListPage{Items: entries, ListRevision: rev}
	if len(entries) > req.Limit {
		page.Items = entries[:req.Limit]
		last := page.Items[req.Limit-1]
		next, encErr := cursorCodec.Encode("media.list", PageCursor{
			Sort:     req.Sort,
			Values:   []CursorValue{{Kind: "int", Value: i64toa(rev)}, {Kind: "int", Value: i64toa(last.Position)}},
			ID:       last.ItemID,
			IssuedAt: time.Now(),
		})
		if encErr == nil {
			page.NextCursor = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

func handleAddToMyList(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		ItemID string `json:"itemId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !validJellyfinID(body.ItemID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid item id is required.")
		return
	}
	// Only an authorized item may be added.
	item := AuthorizedMediaItem{ID: body.ItemID}
	if jellyfinAuthorizer != nil {
		authorized, err := jellyfinAuthorizer.AuthorizeItem(r.Context(), body.ItemID)
		if err != nil {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		item = authorized
	}
	if err := AddToList(r.Context(), user.ID, item); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleRemoveFromMyList(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	itemID := r.PathValue("itemID")
	if !validJellyfinID(itemID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid item id is required.")
		return
	}
	if err := RemoveFromList(r.Context(), user.ID, itemID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleReorderMyList(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		ItemID           string  `json:"itemId"`
		BeforeItemID     *string `json:"beforeItemId"`
		ExpectedRevision int64   `json:"expectedRevision"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !validJellyfinID(body.ItemID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid item id is required.")
		return
	}
	newRev, err := ReorderList(r.Context(), user.ID, body.ItemID, body.BeforeItemID, body.ExpectedRevision)
	if errors.Is(err, errListStaleRevision) {
		writeAPIError(w, r, http.StatusConflict, "list_changed", "Your list changed; reload and retry.")
		return
	}
	if errors.Is(err, errListItemMissing) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"listRevision": newRev})
}
