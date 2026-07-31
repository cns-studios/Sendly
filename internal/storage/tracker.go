package storage

import (
	"context"
	"time"
)

const (
	CounterTotalUploaded  = "total_uploaded"
	CounterTotalProcessed = "total_processed"
)

type TrackingCounters struct {
	TotalUploadedBytes      int64
	TotalUploadedUpdatedAt  time.Time
	TotalProcessedBytes     int64
	TotalProcessedUpdatedAt time.Time
}

type UploadByIP struct {
	IP            string    `db:"ip"`
	UploadedBytes int64     `db:"uploaded_bytes"`
	UpdatedAt     time.Time `db:"updated_at"`
}

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

func (p *Postgres) GetTrackingCounters(ctx context.Context) (*TrackingCounters, error) {
	counters := &TrackingCounters{}

	rows, err := p.db.QueryContext(ctx, `
		SELECT key, value, updated_at
		FROM data_counters
		WHERE key IN ($1, $2)
	`, CounterTotalUploaded, CounterTotalProcessed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value int64
		var updatedAt time.Time
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return nil, err
		}
		switch key {
		case CounterTotalUploaded:
			counters.TotalUploadedBytes = value
			counters.TotalUploadedUpdatedAt = updatedAt
		case CounterTotalProcessed:
			counters.TotalProcessedBytes = value
			counters.TotalProcessedUpdatedAt = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return counters, nil
}

func (p *Postgres) GetUploadsByIP(ctx context.Context, limit, offset int) ([]UploadByIP, error) {
	items := []UploadByIP{}
	err := p.db.SelectContext(ctx, &items, `
		SELECT ip, uploaded_bytes, updated_at
		FROM uploads_by_ip
		ORDER BY uploaded_bytes DESC, ip ASC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	return items, err
}

func (p *Postgres) GetUploadsByIPCount(ctx context.Context) (int, error) {
	var count int
	err := p.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM uploads_by_ip`)
	return count, err
}
