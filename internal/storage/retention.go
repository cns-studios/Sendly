package storage

import (
	"context"
	"time"

	"sendly/internal/models"

	"github.com/lib/pq"
)

// Retention purges hard-delete rows in batches. Each method deletes at most
// batchSize rows per statement and returns how many it deleted, so callers
// loop until it returns less than batchSize. All of them are idempotent.

// PurgeDeletedFiles hard-deletes files soft-deleted before cutoff together
// with the rows that depend on them. Live files are never selected.
func (p *Postgres) PurgeDeletedFiles(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var ids []string
	if err := tx.SelectContext(ctx, &ids, `
		SELECT id FROM files
		WHERE is_deleted = TRUE AND COALESCE(deleted_at, expires_at) < $1
		ORDER BY id
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, cutoff, batchSize); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	for _, table := range []string{
		"file_key_envelopes",
		"file_recipient_key_envelopes",
		"file_access_key_envelopes",
		"file_transfers",
		"desktop_files",
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE file_id = ANY($1)`, pq.Array(ids)); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id = ANY($1) AND is_deleted = TRUE`, pq.Array(ids))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// PurgeUploadIPs deletes per-IP upload totals not updated since cutoff.
func (p *Postgres) PurgeUploadIPs(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM uploads_by_ip
		WHERE ip IN (
			SELECT ip FROM uploads_by_ip WHERE updated_at < $1 ORDER BY updated_at LIMIT $2
		)
	`, cutoff, batchSize)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeClosedReports deletes reports created before cutoff whose file is no
// longer live (deleted, expired or already gone). Reports on live files are
// kept, as moderation may still be pending.
func (p *Postgres) PurgeClosedReports(ctx context.Context, cutoff, now time.Time, batchSize int) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM reports
		WHERE id IN (
			SELECT r.id FROM reports r
			WHERE r.created_at < $1
			  AND NOT EXISTS (
				SELECT 1 FROM files f
				WHERE f.id = r.file_id AND f.is_deleted = FALSE AND f.expires_at > $2
			  )
			ORDER BY r.id
			LIMIT $3
		)
	`, cutoff, now, batchSize)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeTunnelRejections deletes rejections created before cutoff. Those of
// tunnels that are still open are kept, because they keep a rejected guest out.
func (p *Postgres) PurgeTunnelRejections(ctx context.Context, cutoff, now time.Time, batchSize int) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM tunnel_rejections
		WHERE id IN (
			SELECT tr.id FROM tunnel_rejections tr
			JOIN tunnels t ON t.id = tr.tunnel_id
			WHERE tr.created_at < $1
			  AND (t.expires_at <= $2 OR t.status IN ($3, $4))
			ORDER BY tr.id
			LIMIT $5
		)
	`, cutoff, now, models.TunnelStatusEnded, models.TunnelStatusExpired, batchSize)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
