package storage

import (
	"context"
)

const (
	CounterTotalUploaded  = "total_uploaded"
	CounterTotalProcessed = "total_processed"
)

func (p *Postgres) RecordUpload(ctx context.Context, bytes int64, ip string) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE data_counters
		SET value = value + $1, updated_at = NOW()
		WHERE key = $2
	`, bytes, CounterTotalUploaded); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE data_counters
		SET value = value + $1, updated_at = NOW()
		WHERE key = $2
	`, bytes, CounterTotalProcessed); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO uploads_by_ip (ip, uploaded_bytes)
		VALUES ($1, $2)
		ON CONFLICT (ip) DO UPDATE
		SET uploaded_bytes = uploads_by_ip.uploaded_bytes + EXCLUDED.uploaded_bytes,
			updated_at = NOW()
	`, ip, bytes); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (p *Postgres) RecordDownload(ctx context.Context, bytes int64) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE data_counters
		SET value = value + $1, updated_at = NOW()
		WHERE key = $2
	`, bytes, CounterTotalProcessed)
	return err
}
