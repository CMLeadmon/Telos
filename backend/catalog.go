package main

import (
	"context"
	"errors"
	"fmt"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type CatalogProvider string
type CatalogSurface string
type CatalogKind string

const (
	ProviderGrimmory CatalogProvider = "grimmory"
	ProviderJellyfin CatalogProvider = "jellyfin"
	SurfaceLibrary   CatalogSurface  = "library"
	SurfaceStream    CatalogSurface  = "stream"
)

type CatalogObservation struct {
	Provider   CatalogProvider
	UpstreamID string
	LibraryID  string
	Surface    CatalogSurface
	Kind       CatalogKind
}

type CatalogResolution struct {
	ID         string
	Surface    CatalogSurface
	Kind       CatalogKind
	Provider   CatalogProvider
	UpstreamID string
	LibraryID  string
	Active     bool
	Available  bool
	Revision   int64
}

type CatalogScanReport struct {
	Seen     int
	Restored int64
	Missing  int64
}

var (
	errCatalogInvalid      = errors.New("catalog observation invalid")
	errCatalogNotFound     = errors.New("catalog item not found")
	errCatalogWrongSurface = errors.New("catalog item belongs to another surface")
)

var validCatalogKinds = map[CatalogKind]struct{}{
	"epub": {}, "pdf": {}, "audiobook": {}, "video": {}, "audio": {}, "folder": {},
}

func validCatalogProvider(provider CatalogProvider) bool {
	return provider == ProviderGrimmory || provider == ProviderJellyfin
}

func validCatalogSurface(surface CatalogSurface) bool {
	return surface == SurfaceLibrary || surface == SurfaceStream
}

func validCatalogUpstreamString(value string) bool {
	if value == "" || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (in CatalogObservation) Validate() error {
	if !validCatalogProvider(in.Provider) {
		return fmt.Errorf("%w: provider", errCatalogInvalid)
	}
	if !validCatalogUpstreamString(in.UpstreamID) || !validCatalogUpstreamString(in.LibraryID) {
		return fmt.Errorf("%w: upstream identifier", errCatalogInvalid)
	}
	if !validCatalogSurface(in.Surface) {
		return fmt.Errorf("%w: surface", errCatalogInvalid)
	}
	if _, ok := validCatalogKinds[in.Kind]; !ok {
		return fmt.Errorf("%w: kind", errCatalogInvalid)
	}
	return nil
}

type CatalogRepository struct{ db DBTX }

func NewCatalogRepository(db DBTX) *CatalogRepository { return &CatalogRepository{db: db} }

type catalogBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func (r *CatalogRepository) Observe(ctx context.Context, in CatalogObservation) (out CatalogResolution, err error) {
	if err = in.Validate(); err != nil {
		return CatalogResolution{}, err
	}
	started := catalogMetricNow()
	outcome := CatalogOutcomeFailed
	defer func() {
		result := CatalogResultSuccess
		if err != nil {
			result = CatalogResultFailed
		} else {
			_ = recordCatalogReconciled(in.Provider, outcome, 1)
		}
		if err != nil {
			_ = recordCatalogReconciled(in.Provider, CatalogOutcomeFailed, 1)
		}
		_ = observeCatalogReconcileDuration(in.Provider, CatalogOperationObserve, result, catalogMetricSince(started))
	}()

	if out, outcome, err = r.refresh(ctx, in); !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}

	beginner, ok := r.db.(catalogBeginner)
	if !ok {
		return CatalogResolution{}, errors.New("catalog repository requires transaction-capable database")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return CatalogResolution{}, err
	}
	defer tx.Rollback(ctx)

	var id string
	var revision int64
	if err = tx.QueryRow(ctx, `
		INSERT INTO catalog_items (surface, kind)
		VALUES ($1, $2)
		RETURNING id::text, revision`, string(in.Surface), string(in.Kind)).Scan(&id, &revision); err != nil {
		return CatalogResolution{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO catalog_sources
			(catalog_item_id, provider, upstream_id, upstream_library_id)
		VALUES ($1::uuid, $2, $3, $4)`, id, string(in.Provider), in.UpstreamID, in.LibraryID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			_ = tx.Rollback(ctx)
			out, outcome, err = r.refresh(ctx, in)
			return out, err
		}
		return CatalogResolution{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CatalogResolution{}, err
	}
	outcome = CatalogOutcomeCreated
	return CatalogResolution{
		ID: id, Surface: in.Surface, Kind: in.Kind, Provider: in.Provider,
		UpstreamID: in.UpstreamID, LibraryID: in.LibraryID,
		Active: true, Available: true, Revision: revision,
	}, nil
}

func (r *CatalogRepository) refresh(ctx context.Context, in CatalogObservation) (CatalogResolution, CatalogReconcileOutcome, error) {
	var out CatalogResolution
	var wasAvailable bool
	err := r.db.QueryRow(ctx, `
		WITH prior AS (
			SELECT catalog_item_id, provider, upstream_id, available
			FROM catalog_sources
			WHERE provider = $1 AND upstream_id = $2
			FOR UPDATE
		)
		UPDATE catalog_sources AS source
		SET upstream_library_id = $3,
			available = true,
			missing_since = NULL,
			last_seen_at = now()
		FROM prior, catalog_items AS item
		WHERE source.catalog_item_id = prior.catalog_item_id
		  AND source.provider = prior.provider
		  AND source.upstream_id = prior.upstream_id
		  AND item.id = source.catalog_item_id
		RETURNING item.id::text, item.surface, item.kind, source.provider,
			source.upstream_id, source.upstream_library_id, source.active,
			source.available, item.revision, prior.available`,
		string(in.Provider), in.UpstreamID, in.LibraryID).Scan(
		&out.ID, &out.Surface, &out.Kind, &out.Provider, &out.UpstreamID,
		&out.LibraryID, &out.Active, &out.Available, &out.Revision, &wasAvailable)
	if err != nil {
		return CatalogResolution{}, CatalogOutcomeFailed, err
	}
	if wasAvailable {
		return out, CatalogOutcomeRefreshed, nil
	}
	return out, CatalogOutcomeRestored, nil
}

func (r *CatalogRepository) CompleteScan(ctx context.Context, provider CatalogProvider, libraryID string, seenUpstreamIDs []string) (report CatalogScanReport, err error) {
	if !validCatalogProvider(provider) || !validCatalogUpstreamString(libraryID) || len(seenUpstreamIDs) > 100000 {
		return CatalogScanReport{}, fmt.Errorf("%w: completed scan", errCatalogInvalid)
	}
	seen := make([]string, 0, len(seenUpstreamIDs))
	unique := make(map[string]struct{}, len(seenUpstreamIDs))
	for _, id := range seenUpstreamIDs {
		if !validCatalogUpstreamString(id) {
			return CatalogScanReport{}, fmt.Errorf("%w: seen upstream identifier", errCatalogInvalid)
		}
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		seen = append(seen, id)
	}
	report.Seen = len(seen)
	started := catalogMetricNow()
	defer func() {
		result := CatalogResultSuccess
		if err != nil {
			result = CatalogResultFailed
			_ = recordCatalogReconciled(provider, CatalogOutcomeFailed, 1)
		}
		_ = observeCatalogReconcileDuration(provider, CatalogOperationScan, result, catalogMetricSince(started))
	}()

	beginner, ok := r.db.(catalogBeginner)
	if !ok {
		return CatalogScanReport{}, errors.New("catalog repository requires transaction-capable database")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return CatalogScanReport{}, err
	}
	defer tx.Rollback(ctx)

	restored, err := tx.Exec(ctx, `
		UPDATE catalog_sources
		SET available = true, missing_since = NULL, last_seen_at = now()
		WHERE provider = $1 AND upstream_library_id = $2
		  AND upstream_id = ANY($3::text[]) AND NOT available`, string(provider), libraryID, seen)
	if err != nil {
		return CatalogScanReport{}, err
	}
	report.Restored = restored.RowsAffected()

	refreshed, err := tx.Exec(ctx, `
		UPDATE catalog_sources
		SET missing_since = NULL, last_seen_at = now()
		WHERE provider = $1 AND upstream_library_id = $2
		  AND upstream_id = ANY($3::text[]) AND available`, string(provider), libraryID, seen)
	if err != nil {
		return CatalogScanReport{}, err
	}

	missing, err := tx.Exec(ctx, `
		UPDATE catalog_sources
		SET available = false, missing_since = now()
		WHERE provider = $1 AND upstream_library_id = $2 AND available
		  AND NOT (upstream_id = ANY($3::text[]))`, string(provider), libraryID, seen)
	if err != nil {
		return CatalogScanReport{}, err
	}
	report.Missing = missing.RowsAffected()
	if err = tx.Commit(ctx); err != nil {
		return CatalogScanReport{}, err
	}
	if report.Restored > 0 {
		_ = recordCatalogReconciled(provider, CatalogOutcomeRestored, float64(report.Restored))
	}
	refreshedCount := refreshed.RowsAffected() - report.Restored
	if refreshedCount > 0 {
		_ = recordCatalogReconciled(provider, CatalogOutcomeRefreshed, float64(refreshedCount))
	}
	if report.Missing > 0 {
		_ = recordCatalogReconciled(provider, CatalogOutcomeMissing, float64(report.Missing))
	}
	return report, nil
}

func (r *CatalogRepository) Resolve(ctx context.Context, rawID string) (CatalogResolution, error) {
	if !validCatalogUpstreamString(rawID) {
		return CatalogResolution{}, fmt.Errorf("%w: identifier", errCatalogInvalid)
	}
	if looksLikeUUID(rawID) {
		out, err := r.resolveCanonical(ctx, rawID)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, errCatalogNotFound) {
			return CatalogResolution{}, err
		}
	}

	rows, err := r.db.Query(ctx, `
		SELECT catalog_item_id::text, provider
		FROM catalog_sources
		WHERE upstream_id = $1
		ORDER BY provider, catalog_item_id
		LIMIT 3`, rawID)
	if err != nil {
		return CatalogResolution{}, err
	}
	defer rows.Close()
	type legacyMatch struct {
		itemID   string
		provider CatalogProvider
	}
	var matches []legacyMatch
	for rows.Next() {
		var match legacyMatch
		if err := rows.Scan(&match.itemID, &match.provider); err != nil {
			return CatalogResolution{}, err
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return CatalogResolution{}, err
	}
	switch len(matches) {
	case 0:
		return CatalogResolution{}, errCatalogNotFound
	case 1:
		return r.resolveCanonical(ctx, matches[0].itemID)
	default:
		providers := make(map[CatalogProvider]struct{}, len(matches))
		for _, match := range matches {
			providers[match.provider] = struct{}{}
		}
		for provider := range providers {
			_ = recordCatalogReconciled(provider, CatalogOutcomeAmbiguous, 1)
		}
		return CatalogResolution{}, fmt.Errorf("%w: legacy identifier is ambiguous", errCatalogInvalid)
	}
}

func (r *CatalogRepository) resolveCanonical(ctx context.Context, id string) (CatalogResolution, error) {
	var out CatalogResolution
	err := r.db.QueryRow(ctx, `
		SELECT item.id::text, item.surface, item.kind, source.provider,
			source.upstream_id, source.upstream_library_id, source.active,
			source.available, item.revision
		FROM catalog_items AS item
		JOIN catalog_sources AS source ON source.catalog_item_id = item.id
		WHERE item.id = $1::uuid AND source.active`, id).Scan(
		&out.ID, &out.Surface, &out.Kind, &out.Provider, &out.UpstreamID,
		&out.LibraryID, &out.Active, &out.Available, &out.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogResolution{}, errCatalogNotFound
	}
	return out, err
}

func (r *CatalogRepository) ResolveFor(ctx context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
	if !validCatalogSurface(surface) {
		return CatalogResolution{}, fmt.Errorf("%w: surface", errCatalogInvalid)
	}
	out, err := r.Resolve(ctx, rawID)
	if err != nil {
		return CatalogResolution{}, err
	}
	if out.Surface != surface {
		return CatalogResolution{}, errCatalogWrongSurface
	}
	return out, nil
}
