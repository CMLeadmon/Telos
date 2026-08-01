package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxProgressLocatorBytes = 4 * 1024

var errProgressInvalid = errors.New("member progress invalid")

type ProgressInput struct {
	Locator    json.RawMessage `json:"locator"`
	PositionMS int64           `json:"positionMs"`
	DurationMS int64           `json:"durationMs"`
	Percent    float64         `json:"percent"`
	Completed  bool            `json:"completed"`
}

type MemberProgress struct {
	ProgressInput
	UpdatedAt *time.Time `json:"updatedAt"`
}

type ContinuityRepository struct{ db DBTX }

func NewContinuityRepository(db DBTX) *ContinuityRepository {
	return &ContinuityRepository{db: db}
}

func ValidateProgress(kind CatalogKind, in ProgressInput) (ProgressInput, error) {
	if in.PositionMS < 0 || in.DurationMS < 0 ||
		(in.DurationMS != 0 && in.PositionMS > in.DurationMS) ||
		math.IsNaN(in.Percent) || math.IsInf(in.Percent, 0) || in.Percent < 0 || in.Percent > 1 {
		return ProgressInput{}, fmt.Errorf("%w: bounds", errProgressInvalid)
	}
	if len(in.Locator) == 0 {
		in.Locator = json.RawMessage(`{}`)
	}
	if len(in.Locator) > maxProgressLocatorBytes {
		return ProgressInput{}, fmt.Errorf("%w: locator too large", errProgressInvalid)
	}

	var locator map[string]json.RawMessage
	if err := json.Unmarshal(in.Locator, &locator); err != nil || locator == nil {
		return ProgressInput{}, fmt.Errorf("%w: locator must be an object", errProgressInvalid)
	}

	valid := false
	switch kind {
	case "epub":
		valid = validEPUBProgressLocator(locator)
	case "pdf":
		valid = validPDFProgressLocator(locator)
	case "audiobook":
		valid = validAudiobookProgressLocator(locator)
	case "video", "audio":
		valid = len(locator) == 0
	}
	if !valid {
		return ProgressInput{}, fmt.Errorf("%w: locator does not match %s", errProgressInvalid, kind)
	}
	return in, nil
}

func validEPUBProgressLocator(locator map[string]json.RawMessage) bool {
	if len(locator) != 2 {
		return false
	}
	var cfi string
	if err := json.Unmarshal(locator["cfi"], &cfi); err != nil || strings.TrimSpace(cfi) == "" || len(cfi) > 1024 {
		return false
	}
	return validProgressNumber(locator["fraction"], "0", "1", false)
}

func validPDFProgressLocator(locator map[string]json.RawMessage) bool {
	if len(locator) != 2 || !validProgressNumber(locator["page"], "1", "100000", true) {
		return false
	}
	return validProgressNumber(locator["zoom"], "0.1", "10", false)
}

func validAudiobookProgressLocator(locator map[string]json.RawMessage) bool {
	return len(locator) == 1 && validProgressNumber(locator["trackIndex"], "0", "", true)
}

func validProgressNumber(raw json.RawMessage, min, max string, integer bool) bool {
	var number json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &number) != nil || number.String() == "" {
		return false
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return false
	}
	minimum, ok := new(big.Rat).SetString(min)
	if !ok || value.Cmp(minimum) < 0 {
		return false
	}
	if max != "" {
		maximum, ok := new(big.Rat).SetString(max)
		if !ok || value.Cmp(maximum) > 0 {
			return false
		}
	}
	return !integer || value.IsInt()
}

func (r *ContinuityRepository) Get(ctx context.Context, userID, itemID string) (MemberProgress, error) {
	if !looksLikeUUID(userID) || !looksLikeUUID(itemID) {
		return MemberProgress{}, fmt.Errorf("%w: identifier", errProgressInvalid)
	}
	progress, err := scanMemberProgress(r.db.QueryRow(ctx, `
		SELECT locator, position_ms, duration_ms, percent, completed, updated_at
		FROM member_progress
		WHERE user_id = $1::uuid AND catalog_item_id = $2::uuid`, userID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberProgress{}, nil
	}
	return progress, err
}

func (r *ContinuityRepository) GetMany(ctx context.Context, userID string, itemIDs []string) (map[string]MemberProgress, error) {
	if !looksLikeUUID(userID) {
		return nil, fmt.Errorf("%w: user identifier", errProgressInvalid)
	}
	capacity := len(itemIDs)
	if capacity > 500 {
		capacity = 500
	}
	unique := make([]string, 0, capacity)
	seen := make(map[string]struct{}, capacity)
	for _, itemID := range itemIDs {
		if !looksLikeUUID(itemID) {
			return nil, fmt.Errorf("%w: catalog item identifier", errProgressInvalid)
		}
		if _, exists := seen[itemID]; exists {
			continue
		}
		if len(unique) == 500 {
			return nil, fmt.Errorf("%w: too many catalog items", errProgressInvalid)
		}
		seen[itemID] = struct{}{}
		unique = append(unique, itemID)
	}

	out := make(map[string]MemberProgress, len(unique))
	if len(unique) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT catalog_item_id::text, locator, position_ms, duration_ms, percent,
			completed, updated_at
		FROM member_progress
		WHERE user_id = $1::uuid AND catalog_item_id = ANY($2::uuid[])`, userID, unique)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID string
		var progress MemberProgress
		var updatedAt time.Time
		if err := rows.Scan(&itemID, &progress.Locator, &progress.PositionMS,
			&progress.DurationMS, &progress.Percent, &progress.Completed, &updatedAt); err != nil {
			return nil, err
		}
		progress.UpdatedAt = &updatedAt
		out[itemID] = progress
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ContinuityRepository) Put(ctx context.Context, userID, itemID string, in ProgressInput) (MemberProgress, error) {
	if !looksLikeUUID(userID) || !looksLikeUUID(itemID) {
		return MemberProgress{}, fmt.Errorf("%w: identifier", errProgressInvalid)
	}
	if len(in.Locator) == 0 {
		in.Locator = json.RawMessage(`{}`)
	}
	return scanMemberProgress(r.db.QueryRow(ctx, `
		INSERT INTO member_progress
			(user_id, catalog_item_id, locator, position_ms, duration_ms, percent, completed)
		VALUES ($1::uuid, $2::uuid, $3::jsonb, $4, $5, $6, $7)
		ON CONFLICT (user_id, catalog_item_id) DO UPDATE SET
			locator = EXCLUDED.locator,
			position_ms = EXCLUDED.position_ms,
			duration_ms = EXCLUDED.duration_ms,
			percent = EXCLUDED.percent,
			completed = EXCLUDED.completed,
			updated_at = now()
		RETURNING locator, position_ms, duration_ms, percent, completed, updated_at`,
		userID, itemID, in.Locator, in.PositionMS, in.DurationMS, in.Percent, in.Completed))
}

func (r *ContinuityRepository) Continue(ctx context.Context, userID string, surface CatalogSurface, limit int) ([]string, error) {
	if !looksLikeUUID(userID) || !validCatalogSurface(surface) {
		return nil, fmt.Errorf("%w: continue scope", errProgressInvalid)
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT progress.catalog_item_id::text
		FROM member_progress AS progress
		JOIN catalog_items AS item ON item.id = progress.catalog_item_id
		WHERE progress.user_id = $1::uuid
		  AND item.surface = $2
		  AND progress.completed = false
		ORDER BY progress.updated_at DESC, progress.catalog_item_id
		LIMIT $3`, userID, string(surface), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var itemID string
		if err := rows.Scan(&itemID); err != nil {
			return nil, err
		}
		out = append(out, itemID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type progressRow interface {
	Scan(dest ...any) error
}

func scanMemberProgress(row progressRow) (MemberProgress, error) {
	var progress MemberProgress
	var updatedAt time.Time
	if err := row.Scan(&progress.Locator, &progress.PositionMS, &progress.DurationMS,
		&progress.Percent, &progress.Completed, &updatedAt); err != nil {
		return MemberProgress{}, err
	}
	progress.UpdatedAt = &updatedAt
	return progress, nil
}
