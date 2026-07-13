package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ═══════════════════════════════════════════════════════════════════════════
// Settings — validation & guardrail helpers
// ═══════════════════════════════════════════════════════════════════════════

func validateDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > 64 {
		return "", errors.New("display name must be at most 64 characters")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", errors.New("display name contains invalid characters")
		}
	}
	return s, nil
}

func validateNewPassword(s string) error {
	if len(s) < 15 || len(s) > 128 {
		return errors.New("password must be 15-128 characters")
	}
	return nil
}

var validThemes = map[string]bool{"synthwave": true, "ink": true}

func validatePreferences(raw []byte) (map[string]any, error) {
	if len(raw) > 2048 {
		return nil, errors.New("preferences payload too large")
	}
	var prefs map[string]any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return nil, errors.New("invalid JSON")
	}
	for k, v := range prefs {
		switch k {
		case "theme":
			s, ok := v.(string)
			if !ok || !validThemes[s] {
				return nil, errors.New("invalid theme")
			}
		case "sceneEnabled", "reducedMotion",
			"voiceNoiseSuppression", "voiceEchoCancellation", "voiceAutoGainControl":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
		case "voiceInputDeviceId", "voiceOutputDeviceId":
			s, ok := v.(string)
			if !ok || len(s) > 256 {
				return nil, fmt.Errorf("%s must be a string of at most 256 characters", k)
			}
		case "voiceInputGain":
			f, ok := v.(float64)
			if !ok || f < 0 || f > 2 {
				return nil, errors.New("voiceInputGain must be a number between 0 and 2")
			}
		case "voiceOutputVolume":
			f, ok := v.(float64)
			if !ok || f < 0 || f > 1 {
				return nil, errors.New("voiceOutputVolume must be a number between 0 and 1")
			}
		default:
			return nil, fmt.Errorf("unknown preference key %q", k)
		}
	}
	return prefs, nil
}

func slugifyRoleID(name string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if len(slug) < 2 || len(slug) > 50 {
		return "", errors.New("role name must produce a 2-50 character identifier")
	}
	return slug, nil
}

func containsRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

func roleChangeError(actorID string, actorIsOwner bool, targetID string, targetIsOwner bool, newRoles []string) error {
	if actorID == targetID {
		return errors.New("you cannot change your own roles")
	}
	grantsOwner := containsRole(newRoles, "Owner")
	if (grantsOwner != targetIsOwner) && !actorIsOwner {
		return errors.New("only an Owner can grant or revoke the Owner role")
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — profile & preferences
// ═══════════════════════════════════════════════════════════════════════════

func loadPreferences(ctx context.Context, userID string) (map[string]any, error) {
	var raw []byte
	err := dbPool.QueryRow(ctx, `
		SELECT prefs FROM user_preferences WHERE user_id = $1
	`, userID).Scan(&raw)
	if err != nil {
		return map[string]any{}, nil // no row yet — empty prefs
	}
	var prefs map[string]any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return map[string]any{}, nil
	}
	return prefs, nil
}

func upsertPreferences(ctx context.Context, userID string, prefs map[string]any) error {
	raw, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	_, err = dbPool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, prefs, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET prefs = EXCLUDED.prefs, updated_at = NOW()
	`, userID, raw)
	return err
}

func handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name, err := validateDisplayName(body.DisplayName)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var val any
	if name != "" {
		val = name
	} // nil clears the column
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET display_name = $1 WHERE id = $2
	`, val, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	prefs, err := loadPreferences(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prefs)
}

func handlePutPreferences(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	prefs, err := validatePreferences(raw)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := upsertPreferences(r.Context(), user.ID, prefs); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prefs)
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — admin: members
// ═══════════════════════════════════════════════════════════════════════════

func ownerCount(ctx context.Context) (int, error) {
	var n int
	err := dbPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_roles WHERE role_id = 'Owner'`).Scan(&n)
	return n, err
}

func userIsOwner(ctx context.Context, userID string) (bool, error) {
	var is bool
	err := dbPool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = $1 AND role_id = 'Owner')
	`, userID).Scan(&is)
	return is, err
}

// anonymizeUser is the "hard delete": the users row survives (messages keep
// their FK and render as "Deleted User"), but the account becomes unusable.
func anonymizeUser(ctx context.Context, userID string) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	suffix := userID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET username = 'deleted-' || $2, display_name = 'Deleted User',
			password_hash = '!', active = FALSE, avatar_file_id = NULL
		WHERE id = $1
	`, userID, suffix); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_preferences WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AdminUserResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Active      bool     `json:"active"`
	CreatedAt   string   `json:"createdAt"`
	Roles       []string `json:"roles"`
	HasAvatar   bool     `json:"hasAvatar"`
}

func handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT u.id, u.username, COALESCE(u.display_name, ''), u.active, u.created_at,
			COALESCE(array_agg(ur.role_id) FILTER (WHERE ur.role_id IS NOT NULL), '{}'),
			u.avatar_file_id IS NOT NULL
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at ASC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	users := []AdminUserResponse{}
	for rows.Next() {
		var u AdminUserResponse
		var created time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Active, &created, &u.Roles, &u.HasAvatar); err != nil {
			continue
		}
		u.CreatedAt = created.Format(time.RFC3339)
		users = append(users, u)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func handleAdminSetUserRoles(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	var body struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	targetOwner, err := userIsOwner(ctx, targetID)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err := roleChangeError(actor.ID, containsRole(actor.Roles, "Owner"), targetID, targetOwner, body.Roles); err != nil {
		http.Error(w, "Forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	// Last-Owner protection: demoting the only Owner bricks the instance.
	if targetOwner && !containsRole(body.Roles, "Owner") {
		if n, err := ownerCount(ctx); err != nil || n <= 1 {
			http.Error(w, "Forbidden: cannot remove the last Owner", http.StatusForbidden)
			return
		}
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, targetID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, role := range body.Roles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, targetID, role); err != nil {
			http.Error(w, "Bad Request: unknown role "+role, http.StatusBadRequest)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminSetUserActive(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		http.Error(w, "Forbidden: you cannot disable your own account", http.StatusForbidden)
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if !body.Active {
		if isOwner, _ := userIsOwner(ctx, targetID); isOwner {
			if !containsRole(actor.Roles, "Owner") {
				http.Error(w, "Forbidden: only an Owner can disable an Owner", http.StatusForbidden)
				return
			}
			if n, _ := ownerCount(ctx); n <= 1 {
				http.Error(w, "Forbidden: cannot disable the last Owner", http.StatusForbidden)
				return
			}
		}
	}
	tag, err := dbPool.Exec(ctx, `UPDATE users SET active = $1 WHERE id = $2`, body.Active, targetID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if !body.Active {
		dbPool.Exec(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, targetID)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := r.Context().Value(userContextKey).(*UserContext)
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		http.Error(w, "Forbidden: you cannot delete your own account", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	if isOwner, _ := userIsOwner(ctx, targetID); isOwner {
		if !containsRole(actor.Roles, "Owner") {
			http.Error(w, "Forbidden: only an Owner can delete an Owner", http.StatusForbidden)
			return
		}
		if n, _ := ownerCount(ctx); n <= 1 {
			http.Error(w, "Forbidden: cannot delete the last Owner", http.StatusForbidden)
			return
		}
	}
	if err := anonymizeUser(ctx, targetID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — admin: invites
// ═══════════════════════════════════════════════════════════════════════════

type InviteResponse struct {
	ID        string `json:"id"`
	Creator   string `json:"creator"`
	RoleID    string `json:"roleId"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

func handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT i.token_hash, COALESCE(u.username, '(deleted)'), COALESCE(i.role_id, 'Member'),
			i.created_at, i.expires_at
		FROM invites i
		LEFT JOIN users u ON i.creator_id = u.id
		WHERE i.used_at IS NULL AND i.expires_at > NOW()
		ORDER BY i.created_at DESC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	invites := []InviteResponse{}
	for rows.Next() {
		var inv InviteResponse
		var created, expires time.Time
		if err := rows.Scan(&inv.ID, &inv.Creator, &inv.RoleID, &created, &expires); err != nil {
			continue
		}
		inv.CreatedAt = created.Format(time.RFC3339)
		inv.ExpiresAt = expires.Format(time.RFC3339)
		invites = append(invites, inv)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(invites)
}

func handleAdminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tag, err := dbPool.Exec(r.Context(), `
		DELETE FROM invites WHERE token_hash = $1 AND used_at IS NULL
	`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "Invite not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — admin: roles & permissions
// ═══════════════════════════════════════════════════════════════════════════

type RoleResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Builtin     bool     `json:"builtin"`
	MemberCount int      `json:"memberCount"`
	Permissions []string `json:"permissions"`
}

func handleAdminListRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT ro.id, ro.name, ro.builtin,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = ro.id),
			COALESCE((SELECT array_agg(rp.permission_id) FROM role_permissions rp WHERE rp.role_id = ro.id), '{}')
		FROM roles ro
		ORDER BY ro.builtin DESC, ro.id ASC
	`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	roles := []RoleResponse{}
	for rows.Next() {
		var role RoleResponse
		if err := rows.Scan(&role.ID, &role.Name, &role.Builtin, &role.MemberCount, &role.Permissions); err != nil {
			continue
		}
		roles = append(roles, role)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func handleAdminListPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `SELECT id, COALESCE(description, '') FROM permissions ORDER BY id`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type perm struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	perms := []perm{}
	for rows.Next() {
		var p perm
		if err := rows.Scan(&p.ID, &p.Description); err == nil {
			perms = append(perms, p)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(perms)
}

func setRolePermissions(ctx context.Context, roleID string, perms []string) error {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, roleID, p); err != nil {
			return fmt.Errorf("unknown permission %q", p)
		}
	}
	return tx.Commit(ctx)
}

func handleAdminCreateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	id, err := slugifyRoleID(body.Name)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err := dbPool.Exec(ctx, `
		INSERT INTO roles (id, name, builtin) VALUES ($1, $2, FALSE)
	`, id, strings.TrimSpace(body.Name)); err != nil {
		http.Error(w, "Conflict: role already exists", http.StatusConflict)
		return
	}
	if len(body.Permissions) > 0 {
		if err := setRolePermissions(ctx, id, body.Permissions); err != nil {
			http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(RoleResponse{ID: id, Name: strings.TrimSpace(body.Name), Permissions: body.Permissions})
}

func handleAdminSetRolePermissions(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("id")
	if roleID == "Owner" {
		http.Error(w, "Forbidden: the Owner role is immutable", http.StatusForbidden)
		return
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	var exists bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1)`, roleID).Scan(&exists); err != nil || !exists {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}
	if err := setRolePermissions(r.Context(), roleID, body.Permissions); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleAdminDeleteRole(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("id")
	var builtin bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT builtin FROM roles WHERE id = $1`, roleID).Scan(&builtin); err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}
	if builtin {
		http.Error(w, "Forbidden: built-in roles cannot be deleted", http.StatusForbidden)
		return
	}
	if _, err := dbPool.Exec(r.Context(), `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ═══════════════════════════════════════════════════════════════════════════
// Settings — avatars
// ═══════════════════════════════════════════════════════════════════════════

var avatarExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

func handleUploadAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	fileID, ok := processUploadWithLimits(w, r, user.ID, false, 5*1024*1024, avatarExts, false)
	if !ok {
		return // error response already written
	}
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET avatar_file_id = $1 WHERE id = $2
	`, fileID, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "success",
		"avatarUrl": "/api/v1/users/" + user.ID + "/avatar",
	})
}

func handleDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	if _, err := dbPool.Exec(r.Context(), `
		UPDATE users SET avatar_file_id = NULL WHERE id = $1
	`, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleGetAvatar(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	var storageKey, mimeType string
	err := dbPool.QueryRow(r.Context(), `
		SELECT f.storage_key, f.mime_type
		FROM users u JOIN files f ON u.avatar_file_id = f.id
		WHERE u.id = $1 AND f.scan_status = 'clean'
	`, userID).Scan(&storageKey, &mimeType)
	if err != nil {
		http.Error(w, "No avatar", http.StatusNotFound)
		return
	}
	filePath := filepath.Join("/data/shared/staging/library", storageKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "No avatar", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, filePath)
}

func currentTokenHash(r *http.Request) string {
	cookie, err := r.Cookie("telos_session")
	if err != nil || cookie.Value == "" {
		return ""
	}
	h := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(h[:])
}

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := validateNewPassword(body.NewPassword); err != nil {
		http.Error(w, "Bad Request: " + err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	var hash string
	if err := dbPool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&hash); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	ok, err := verifyPassword(body.CurrentPassword, hash)
	if err != nil || !ok {
		http.Error(w, "Unauthorized: Current password is incorrect", http.StatusUnauthorized)
		return
	}
	newHash, err := hashPassword(body.NewPassword)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if _, err := dbPool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, newHash, user.ID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// Revoke every other session — a changed password invalidates old devices.
	dbPool.Exec(ctx, `
		UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND token_hash <> $2
	`, user.ID, currentTokenHash(r))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type SessionResponse struct {
	ID         string  `json:"id"`
	CreatedAt  string  `json:"createdAt"`
	LastSeenAt *string `json:"lastSeenAt"`
	UserAgent  string  `json:"userAgent"`
	ClientIP   string  `json:"clientIp"`
	Current    bool    `json:"current"`
}

func handleListMySessions(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	cur := currentTokenHash(r)
	rows, err := dbPool.Query(r.Context(), `
		SELECT token_hash, created_at, last_seen_at, user_agent, client_ip
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, user.ID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	sessions := []SessionResponse{}
	for rows.Next() {
		var s SessionResponse
		var created time.Time
		var lastSeen *time.Time
		if err := rows.Scan(&s.ID, &created, &lastSeen, &s.UserAgent, &s.ClientIP); err != nil {
			continue
		}
		s.CreatedAt = created.Format(time.RFC3339)
		if lastSeen != nil {
			v := lastSeen.Format(time.RFC3339)
			s.LastSeenAt = &v
		}
		s.Current = s.ID == cur
		sessions = append(sessions, s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id := r.PathValue("id")
	tag, err := dbPool.Exec(r.Context(), `
		UPDATE sessions SET revoked_at = NOW()
		WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, user.ID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	tag, err := dbPool.Exec(r.Context(), `
		UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND token_hash <> $2
	`, user.ID, currentTokenHash(r))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"revoked": tag.RowsAffected()})
}
