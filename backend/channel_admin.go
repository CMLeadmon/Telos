package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// OverrideDecision is inherit (no row), allow, or deny.
type OverrideDecision string

const (
	DecisionInherit OverrideDecision = "inherit"
	DecisionAllow   OverrideDecision = "allow"
	DecisionDeny    OverrideDecision = "deny"
)

var (
	errChannelSlugInvalid  = errors.New("channel slug is invalid")
	errChannelTypeInvalid  = errors.New("channel type is invalid")
	errChannelNameTaken    = errors.New("channel name is taken")
	errChannelInUseByParty = errors.New("channel is linked to an active watch party")
)

// normalizeChannelSlug lowercases and validates a channel name/slug.
func normalizeChannelSlug(name string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(name))
	if len(s) < 2 || len(s) > 50 {
		return "", errChannelSlugInvalid
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", errChannelSlugInvalid
		}
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return "", errChannelSlugInvalid
	}
	return s, nil
}

func validChannelType(t string) bool { return t == "text" || t == "voice" }

func channelAudit(ctx context.Context, q DBTX, actorID, channelID, action string, detail map[string]any) error {
	payload, _ := marshalAuditDetail(detail)
	_, err := q.Exec(ctx, `INSERT INTO channel_audit (actor_id, channel_id, action, detail) VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3, $4)`, actorID, channelID, action, payload)
	return err
}

// CreateChannel creates a channel with a normalized slug and valid type.
func CreateChannel(ctx context.Context, actorID, name, chType string) (string, error) {
	slug, err := normalizeChannelSlug(name)
	if err != nil {
		return "", err
	}
	if !validChannelType(chType) {
		return "", errChannelTypeInvalid
	}
	var id string
	err = dbPool.QueryRow(ctx, `INSERT INTO channels (name, type) VALUES ($1, $2) RETURNING id::text`, slug, chType).Scan(&id)
	if isUniqueViolation(err) {
		return "", errChannelNameTaken
	}
	if err != nil {
		return "", err
	}
	_ = channelAudit(ctx, dbPool, actorID, id, "channel_created", map[string]any{"name": slug, "type": chType})
	return id, nil
}

// UpdateChannel renames a channel (type is immutable to preserve text/voice
// invariants).
func UpdateChannel(ctx context.Context, actorID, id, name string) error {
	slug, err := normalizeChannelSlug(name)
	if err != nil {
		return err
	}
	tag, err := dbPool.Exec(ctx, `UPDATE channels SET name = $2 WHERE id = $1`, id, slug)
	if isUniqueViolation(err) {
		return errChannelNameTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errChannelNotFound
	}
	_ = channelAudit(ctx, dbPool, actorID, id, "channel_updated", map[string]any{"name": slug})
	return nil
}

// DeleteChannel deletes a channel, refusing when it is linked to an active
// Watch Party (409).
func DeleteChannel(ctx context.Context, actorID, id string) error {
	var linked bool
	if err := dbPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM watch_parties WHERE (text_channel_id=$1 OR voice_channel_id=$1) AND ended_at IS NULL)`, id).Scan(&linked); err != nil {
		return err
	}
	if linked {
		return errChannelInUseByParty
	}
	tag, err := dbPool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errChannelNotFound
	}
	_ = channelAudit(ctx, dbPool, actorID, id, "channel_deleted", nil)
	return nil
}

// GetRoleOverrides returns decisions for a channel keyed by role then permission.
func GetRoleOverrides(ctx context.Context, channelID string) (map[string]map[string]OverrideDecision, error) {
	rows, err := dbPool.Query(ctx, `SELECT role_id, permission_id, decision FROM channel_permission_overrides WHERE channel_id = $1`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]OverrideDecision{}
	for rows.Next() {
		var role, perm, dec string
		if err := rows.Scan(&role, &perm, &dec); err != nil {
			return nil, err
		}
		if out[role] == nil {
			out[role] = map[string]OverrideDecision{}
		}
		out[role][perm] = OverrideDecision(dec)
	}
	return out, rows.Err()
}

// SetRoleOverride upserts (or clears, on inherit) one channel/role/permission
// decision, records an audit event, and returns the affected role.
func SetRoleOverride(ctx context.Context, actorID, channelID, roleID, permID string, decision OverrideDecision) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	switch decision {
	case DecisionInherit:
		if _, err := tx.Exec(ctx, `DELETE FROM channel_permission_overrides WHERE channel_id=$1 AND role_id=$2 AND permission_id=$3`, channelID, roleID, permID); err != nil {
			return err
		}
	case DecisionAllow, DecisionDeny:
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_permission_overrides (channel_id, role_id, permission_id, decision)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (channel_id, role_id, permission_id) DO UPDATE SET decision = EXCLUDED.decision
		`, channelID, roleID, permID, string(decision)); err != nil {
			return err
		}
	default:
		return errors.New("invalid decision")
	}
	if err := channelAudit(ctx, tx, actorID, channelID, "override_changed", map[string]any{"role": roleID, "permission": permID, "decision": string(decision)}); err != nil {
		return err
	}
	// Record an admin security intent so affected recipients are notified.
	if err := securityEvents.Record(ctx, tx, SecurityEventIntent{Kind: "channel_override_changed", ActorID: actorID, SubjectID: actorID, ResourceID: channelID}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Affected sockets re-validate channel access on the 30s fallback and close
	// if view was revoked; nudge by revoking the actor's own stale caches is not
	// needed here.
	return nil
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

func handleAdminCreateChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	id, err := CreateChannel(r.Context(), user.ID, body.Name, body.Type)
	if err != nil {
		channelAdminErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": id})
}

func handleAdminUpdateChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if err := UpdateChannel(r.Context(), user.ID, r.PathValue("id"), body.Name); err != nil {
		channelAdminErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleAdminDeleteChannel(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	if err := DeleteChannel(r.Context(), user.ID, r.PathValue("id")); err != nil {
		channelAdminErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleGetChannelOverrides(w http.ResponseWriter, r *http.Request) {
	overrides, err := GetRoleOverrides(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	writeJSON(w, map[string]any{"overrides": overrides})
}

func handlePutChannelOverride(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		RoleID       string `json:"roleId"`
		PermissionID string `json:"permissionId"`
		Decision     string `json:"decision"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	dec := OverrideDecision(body.Decision)
	if dec != DecisionInherit && dec != DecisionAllow && dec != DecisionDeny {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The decision is invalid.")
		return
	}
	if err := SetRoleOverride(r.Context(), user.ID, r.PathValue("id"), body.RoleID, body.PermissionID, dec); err != nil {
		channelAdminErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func channelAdminErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errChannelSlugInvalid), errors.Is(err, errChannelTypeInvalid):
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The channel name or type is invalid.")
	case errors.Is(err, errChannelNameTaken):
		writeAPIError(w, r, http.StatusConflict, "name_taken", "That channel name is taken.")
	case errors.Is(err, errChannelInUseByParty):
		writeAPIError(w, r, http.StatusConflict, "channel_in_use", "This channel is linked to an active Watch Party.")
	case errors.Is(err, errChannelNotFound):
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
	default:
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
}
