package main

import (
	"context"
	"errors"
)

const catalogBackfillBatchSize = 200

const canonicalCatalogIDPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

type CatalogBackfillReport struct {
	Progress    int64
	Annotations int64
	Embeds      int64
	Updated     int64
}

// BackfillCatalogReferences migrates legacy references only after an observed
// source maps them to one catalog item. Each bounded batch commits separately,
// so the operation is safe to resume and remains idempotent.
func BackfillCatalogReferences(ctx context.Context, db DBTX) (CatalogBackfillReport, error) {
	beginner, ok := db.(catalogBeginner)
	if !ok {
		return CatalogBackfillReport{}, errors.New("catalog backfill requires transaction-capable database")
	}

	var report CatalogBackfillReport
	progress, err := runCatalogBackfill(ctx, beginner, backfillProgressSQL)
	report.Progress = progress
	report.Updated = report.Progress
	if err != nil {
		return report, err
	}

	annotations, err := runCatalogBackfill(ctx, beginner, backfillAnnotationsSQL)
	report.Annotations = annotations
	report.Updated = report.Progress + report.Annotations
	if err != nil {
		return report, err
	}

	embeds, err := runCatalogBackfill(ctx, beginner, backfillEmbedsSQL)
	report.Embeds = embeds
	report.Updated = report.Progress + report.Annotations + report.Embeds
	if err != nil {
		return report, err
	}
	return report, nil
}

func runCatalogBackfill(ctx context.Context, beginner catalogBeginner, statement string) (int64, error) {
	var updated int64
	for {
		tx, err := beginner.Begin(ctx)
		if err != nil {
			return updated, err
		}
		tag, err := tx.Exec(ctx, statement, catalogBackfillBatchSize, canonicalCatalogIDPattern)
		if err != nil {
			_ = tx.Rollback(ctx)
			return updated, err
		}
		batch := tag.RowsAffected()
		if err := tx.Commit(ctx); err != nil {
			_ = tx.Rollback(ctx)
			return updated, err
		}
		updated += batch
		if batch == 0 {
			return updated, nil
		}
	}
}

const backfillProgressSQL = `
	WITH candidates AS (
		SELECT progress.user_id, source.catalog_item_id, progress.locator,
			progress.percent, progress.updated_at
		FROM book_progress AS progress
		JOIN catalog_sources AS source
		  ON source.provider = 'grimmory'
		 AND source.upstream_id = progress.book_id::text
		JOIN catalog_items AS item
		  ON item.id = source.catalog_item_id
		 AND item.surface = 'library'
		WHERE NOT EXISTS (
			SELECT 1 FROM member_progress AS current
			WHERE current.user_id = progress.user_id
			  AND current.catalog_item_id = source.catalog_item_id
		)
		  AND progress.percent BETWEEN 0 AND 1
		  AND progress.book_id::text !~* $2
		  AND (
			SELECT count(*) FROM catalog_sources AS collision
			WHERE collision.upstream_id = progress.book_id::text
		  ) = 1
		  AND EXISTS (
			SELECT 1 FROM catalog_sources AS active_source
			WHERE active_source.catalog_item_id = source.catalog_item_id
			  AND active_source.active
		  )
		ORDER BY progress.user_id, progress.book_id
		LIMIT $1
		FOR UPDATE OF progress SKIP LOCKED
	)
	INSERT INTO member_progress
		(user_id, catalog_item_id, locator, percent, completed, updated_at)
	SELECT user_id, catalog_item_id, locator, percent,
		percent >= 1,
		COALESCE(updated_at, now())
	FROM candidates
	ON CONFLICT DO NOTHING`

const backfillAnnotationsSQL = `
	WITH candidates AS (
		SELECT annotation.id, source.catalog_item_id
		FROM annotations AS annotation
		JOIN catalog_sources AS source ON source.upstream_id = annotation.target_id
		JOIN catalog_items AS item ON item.id = source.catalog_item_id
		WHERE annotation.target_id !~* $2
		  AND (
			(annotation.target_type = 'book' AND source.provider = 'grimmory' AND item.surface = 'library')
			OR
			(annotation.target_type = 'media' AND source.provider = 'jellyfin' AND item.surface = 'stream')
		  )
		  AND (
			SELECT count(*) FROM catalog_sources AS collision
			WHERE collision.upstream_id = annotation.target_id
		  ) = 1
		  AND EXISTS (
			SELECT 1 FROM catalog_sources AS active_source
			WHERE active_source.catalog_item_id = source.catalog_item_id
			  AND active_source.active
		  )
		ORDER BY annotation.id
		LIMIT $1
		FOR UPDATE OF annotation SKIP LOCKED
	)
	UPDATE annotations AS annotation
	SET target_id = candidates.catalog_item_id::text
	FROM candidates
	WHERE annotation.id = candidates.id`

const backfillEmbedsSQL = `
	WITH candidates AS (
		SELECT message.id, source.catalog_item_id
		FROM messages AS message
		JOIN catalog_sources AS source ON source.upstream_id = message.embed_ref
		JOIN catalog_items AS item ON item.id = source.catalog_item_id
		WHERE message.embed_ref !~* $2
		  AND (
			(message.embed_kind = 'library_book' AND source.provider = 'grimmory' AND item.surface = 'library')
			OR
			(message.embed_kind = 'stream_film' AND source.provider = 'jellyfin' AND item.surface = 'stream')
		  )
		  AND (
			SELECT count(*) FROM catalog_sources AS collision
			WHERE collision.upstream_id = message.embed_ref
		  ) = 1
		  AND EXISTS (
			SELECT 1 FROM catalog_sources AS active_source
			WHERE active_source.catalog_item_id = source.catalog_item_id
			  AND active_source.active
		  )
		ORDER BY message.id
		LIMIT $1
		FOR UPDATE OF message SKIP LOCKED
	)
	UPDATE messages AS message
	SET embed_ref = candidates.catalog_item_id::text
	FROM candidates
	WHERE message.id = candidates.id`
