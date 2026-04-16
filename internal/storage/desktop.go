package storage

import (
	"context"
	"database/sql"
	"time"

	"shareit/internal/models"
)



func (p *Postgres) CreateDesktopAPIKey(ctx context.Context, key *models.DesktopAPIKey) error {
	query := `
		INSERT INTO desktop_api_keys (id, key_value, owner_name, created_at, is_active)
		VALUES (gen_random_uuid(), $1, $2, $3, TRUE)
		RETURNING id
	`
	return p.db.QueryRowContext(ctx, query, key.KeyValue, key.OwnerName, time.Now()).Scan(&key.ID)
}

func (p *Postgres) GetDesktopAPIKey(ctx context.Context, keyValue string) (*models.DesktopAPIKey, error) {
	var key models.DesktopAPIKey
	query := `SELECT id, key_value, owner_name, created_at, is_active FROM desktop_api_keys WHERE key_value = $1 AND is_active = TRUE`
	err := p.db.GetContext(ctx, &key, query, keyValue)
	if err == sql.ErrNoRows {
		return nil, models.ErrAPIKeyNotFound
	}
	return &key, err
}

func (p *Postgres) GetDesktopAPIKeyByID(ctx context.Context, id string) (*models.DesktopAPIKey, error) {
	var key models.DesktopAPIKey
	query := `SELECT id, key_value, owner_name, created_at, is_active FROM desktop_api_keys WHERE id = $1`
	err := p.db.GetContext(ctx, &key, query, id)
	if err == sql.ErrNoRows {
		return nil, models.ErrAPIKeyNotFound
	}
	return &key, err
}

func (p *Postgres) ListDesktopAPIKeys(ctx context.Context) ([]models.DesktopAPIKey, error) {
	var keys []models.DesktopAPIKey
	query := `SELECT id, key_value, owner_name, created_at, is_active FROM desktop_api_keys ORDER BY created_at DESC`
	err := p.db.SelectContext(ctx, &keys, query)
	return keys, err
}

func (p *Postgres) RevokeDesktopAPIKey(ctx context.Context, keyValue string) error {
	query := `UPDATE desktop_api_keys SET is_active = FALSE WHERE key_value = $1`
	res, err := p.db.ExecContext(ctx, query, keyValue)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return models.ErrAPIKeyNotFound
	}
	return nil
}

func (p *Postgres) RevokeDesktopAPIKeyByID(ctx context.Context, id string) error {
	query := `UPDATE desktop_api_keys SET is_active = FALSE WHERE id = $1`
	res, err := p.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return models.ErrAPIKeyNotFound
	}
	return nil
}



func (p *Postgres) AssociateFileWithKey(ctx context.Context, fileID, apiKeyID string) error {
	query := `INSERT INTO desktop_files (file_id, api_key_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err := p.db.ExecContext(ctx, query, fileID, apiKeyID)
	return err
}

func (p *Postgres) FileOwnedByKey(ctx context.Context, fileID, apiKeyID string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM desktop_files WHERE file_id = $1 AND api_key_id = $2`
	err := p.db.GetContext(ctx, &count, query, fileID, apiKeyID)
	return count > 0, err
}

func (p *Postgres) ListFilesByAPIKey(ctx context.Context, apiKeyID string, limit, offset int) ([]models.DesktopFileMetadata, error) {
	var files []models.DesktopFileMetadata
	query := `
		SELECT
			f.id,
			f.numeric_code,
			f.original_name AS file_name,
			f.size_bytes    AS file_size,
			f.expires_at,
			f.created_at    AS uploaded_at
		FROM files f
		INNER JOIN desktop_files df ON df.file_id = f.id
		WHERE df.api_key_id = $1
		  AND f.is_deleted = FALSE
		  AND f.expires_at > NOW()
		  AND f.tunnel_id IS NULL
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`
	err := p.db.SelectContext(ctx, &files, query, apiKeyID, limit, offset)
	return files, err
}

func (p *Postgres) GetDesktopFileStats(ctx context.Context, apiKeyID string) (count int64, totalSize int64, err error) {
	query := `
		SELECT COUNT(*), COALESCE(SUM(f.size_bytes), 0)
		FROM files f
		INNER JOIN desktop_files df ON df.file_id = f.id
		WHERE df.api_key_id = $1
		  AND f.is_deleted = FALSE
		  AND f.expires_at > NOW()
		  AND f.tunnel_id IS NULL
	`
	row := p.db.QueryRowContext(ctx, query, apiKeyID)
	err = row.Scan(&count, &totalSize)
	return
}