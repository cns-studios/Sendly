package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type Postgres struct {
	db *sqlx.DB
}

func (p *Postgres) CreateFileWithEnvelope(ctx context.Context, file *models.File, envelope *models.FileKeyEnvelope, recipientEnvelopes []models.FileRecipientKeyEnvelope, identityEnvelope *models.FileAccessKeyEnvelope) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	insertFile := `
		INSERT INTO files (
			id,
			numeric_code,
			original_name,
			size_bytes,
			uploader_ip,
			owner_cns_user_id,
			owner_cns_username,
			tunnel_id,
			expires_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	if _, err = tx.ExecContext(ctx, insertFile,
		file.ID,
		file.NumericCode,
		file.OriginalName,
		file.SizeBytes,
		file.UploaderIP,
		file.OwnerCNSUserID,
		file.OwnerCNSUserName,
		file.TunnelID,
		file.ExpiresAt,
		file.CreatedAt,
	); err != nil {
		_ = tx.Rollback()
		return err
	}

	if envelope != nil {
		insertEnvelope := `
			INSERT INTO file_key_envelopes (file_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version)
			VALUES ($1, $2, $3, $4, $5)
		`
		if _, err = tx.ExecContext(ctx, insertEnvelope,
			envelope.FileID,
			envelope.WrappedDEK,
			envelope.DEKWrapAlg,
			envelope.DEKWrapNonce,
			envelope.DEKWrapVersion,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	if len(recipientEnvelopes) > 0 {
		insertRecipientEnvelope := `
			INSERT INTO file_recipient_key_envelopes (
				file_id,
				recipient_cns_user_id,
				recipient_device_id,
				wrapped_dek,
				dek_wrap_alg,
				dek_wrap_nonce,
				dek_wrap_version
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (file_id, recipient_cns_user_id, recipient_device_id)
			DO UPDATE SET
				wrapped_dek = EXCLUDED.wrapped_dek,
				dek_wrap_alg = EXCLUDED.dek_wrap_alg,
				dek_wrap_nonce = EXCLUDED.dek_wrap_nonce,
				dek_wrap_version = EXCLUDED.dek_wrap_version,
				created_at = NOW()
		`
		for _, env := range recipientEnvelopes {
			if _, err = tx.ExecContext(ctx, insertRecipientEnvelope,
				env.FileID,
				env.RecipientCNSUserID,
				env.RecipientDeviceID,
				env.WrappedDEK,
				env.DEKWrapAlg,
				env.DEKWrapNonce,
				env.DEKWrapVersion,
			); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}

	if identityEnvelope != nil {
		if _, err = tx.ExecContext(ctx, `
					INSERT INTO file_access_key_envelopes (
						file_id, recipient_cns_user_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce,
						dek_wrap_version, recipient_key_version, access_kind, source_tunnel_id,
						granted_at, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
					ON CONFLICT (file_id, recipient_cns_user_id) DO UPDATE SET
						wrapped_dek = EXCLUDED.wrapped_dek,
						dek_wrap_alg = EXCLUDED.dek_wrap_alg,
						dek_wrap_nonce = EXCLUDED.dek_wrap_nonce,
						dek_wrap_version = EXCLUDED.dek_wrap_version,
						recipient_key_version = EXCLUDED.recipient_key_version,
						access_kind = EXCLUDED.access_kind,
						source_tunnel_id = EXCLUDED.source_tunnel_id,
						granted_at = EXCLUDED.granted_at,
						updated_at = EXCLUDED.updated_at
				`, identityEnvelope.FileID, identityEnvelope.RecipientCNSUserID,
			identityEnvelope.WrappedDEK, identityEnvelope.DEKWrapAlg, identityEnvelope.DEKWrapNonce,
			identityEnvelope.DEKWrapVersion, identityEnvelope.RecipientKeyVersion, identityEnvelope.AccessKind,
			identityEnvelope.SourceTunnelID, identityEnvelope.GrantedAt, identityEnvelope.CreatedAt,
			identityEnvelope.UpdatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func NewPostgres(cfg *config.Config) (*Postgres, error) {
	db, err := sqlx.Connect("postgres", cfg.PostgresDSN())
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	return &Postgres{db: db}, nil
}

func (p *Postgres) Close() error {
	return p.db.Close()
}

func (p *Postgres) CreateFile(ctx context.Context, file *models.File) error {
	query := `
		INSERT INTO files (
			id,
			numeric_code,
			original_name,
			size_bytes,
			uploader_ip,
			owner_cns_user_id,
			owner_cns_username,
			tunnel_id,
			expires_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := p.db.ExecContext(ctx, query,
		file.ID,
		file.NumericCode,
		file.OriginalName,
		file.SizeBytes,
		file.UploaderIP,
		file.OwnerCNSUserID,
		file.OwnerCNSUserName,
		file.TunnelID,
		file.ExpiresAt,
		file.CreatedAt,
	)
	return err
}

func (p *Postgres) GetFileByID(ctx context.Context, id string) (*models.File, error) {
	var file models.File
	query := `SELECT * FROM files WHERE id = $1`
	err := p.db.GetContext(ctx, &file, query, id)
	if err == sql.ErrNoRows {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}

	if file.IsDeleted {
		return nil, models.ErrFileDeleted
	}

	if time.Now().After(file.ExpiresAt) {
		return nil, models.ErrFileExpired
	}

	return &file, nil
}

func (p *Postgres) GetFileByNumericCode(ctx context.Context, code string) (*models.File, error) {
	var file models.File
	query := `SELECT * FROM files WHERE numeric_code = $1`
	err := p.db.GetContext(ctx, &file, query, code)
	if err == sql.ErrNoRows {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}

	if file.IsDeleted {
		return nil, models.ErrFileDeleted
	}

	if time.Now().After(file.ExpiresAt) {
		return nil, models.ErrFileExpired
	}

	return &file, nil
}

func (p *Postgres) IncrementReportCount(ctx context.Context, fileID string) (int, error) {
	var reportCount int
	query := `
		UPDATE files 
		SET report_count = report_count + 1 
		WHERE id = $1 
		RETURNING report_count
	`
	err := p.db.GetContext(ctx, &reportCount, query, fileID)
	return reportCount, err
}

func (p *Postgres) MarkFileDeleted(ctx context.Context, fileID string) error {
	query := `UPDATE files SET is_deleted = TRUE WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, fileID)
	return err
}

// CreateReport stores a report and reports whether it was new. A signed-in
// user can report a file only once (unique index); a repeat returns false.
func (p *Postgres) CreateReport(ctx context.Context, report *models.Report) (bool, error) {
	query := `
		INSERT INTO reports (file_id, reporter_ip, reporter_cns_user_id, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_id, reporter_cns_user_id) WHERE reporter_cns_user_id IS NOT NULL DO NOTHING
	`
	res, err := p.db.ExecContext(ctx, query,
		report.FileID,
		report.ReporterIP,
		report.ReporterCNSUserID,
		report.CreatedAt,
	)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

// CountSignedInReporters counts the distinct signed-in users who reported a
// file; only these reports count towards automatic deletion.
func (p *Postgres) CountSignedInReporters(ctx context.Context, fileID string) (int, error) {
	var count int
	err := p.db.GetContext(ctx, &count, `
		SELECT COUNT(DISTINCT reporter_cns_user_id)
		FROM reports
		WHERE file_id = $1 AND reporter_cns_user_id IS NOT NULL
	`, fileID)
	return count, err
}

func (p *Postgres) GetReportsByFileID(ctx context.Context, fileID string) ([]models.Report, error) {
	var reports []models.Report
	query := `SELECT * FROM reports WHERE file_id = $1 ORDER BY created_at DESC`
	err := p.db.SelectContext(ctx, &reports, query, fileID)
	return reports, err
}

// HasUserReportedFile de-duplicates reports: by user for signed-in
// reporters (reporterUserID > 0), by client IP for anonymous ones.
func (p *Postgres) HasUserReportedFile(ctx context.Context, fileID, reporterIP string, reporterUserID int64) (bool, error) {
	var count int
	if reporterUserID > 0 {
		err := p.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM reports WHERE file_id = $1 AND reporter_cns_user_id = $2`, fileID, reporterUserID)
		return count > 0, err
	}
	query := `SELECT COUNT(*) FROM reports WHERE file_id = $1 AND reporter_ip = $2 AND reporter_cns_user_id IS NULL`
	err := p.db.GetContext(ctx, &count, query, fileID, reporterIP)
	return count > 0, err
}

func (p *Postgres) GetExpiredFiles(ctx context.Context) ([]models.File, error) {
	var files []models.File
	query := `SELECT * FROM files WHERE expires_at < $1 AND is_deleted = FALSE`
	err := p.db.SelectContext(ctx, &files, query, time.Now())
	return files, err
}

func (p *Postgres) DeleteExpiredFiles(ctx context.Context) (int64, error) {
	query := `UPDATE files SET is_deleted = TRUE WHERE expires_at < $1 AND is_deleted = FALSE`
	result, err := p.db.ExecContext(ctx, query, time.Now())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (p *Postgres) GetDeletedFiles(ctx context.Context) ([]models.File, error) {
	var files []models.File
	query := `SELECT * FROM files WHERE is_deleted = TRUE`
	err := p.db.SelectContext(ctx, &files, query)
	return files, err
}

func (p *Postgres) GetFileForAdmin(ctx context.Context, id string) (*models.File, error) {
	var file models.File
	query := `SELECT * FROM files WHERE id = $1`
	err := p.db.GetContext(ctx, &file, query, id)
	if err == sql.ErrNoRows {
		return nil, models.ErrFileNotFound
	}
	return &file, err
}

func (p *Postgres) GetAllFiles(ctx context.Context, limit, offset int) ([]models.File, error) {
	var files []models.File
	query := `SELECT * FROM files ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	err := p.db.SelectContext(ctx, &files, query, limit, offset)
	return files, err
}

func (p *Postgres) DeleteFilePermanently(ctx context.Context, fileID string) error {
	query := `DELETE FROM files WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, fileID)
	return err
}

func (p *Postgres) NumericCodeExists(ctx context.Context, code string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM files WHERE numeric_code = $1`
	err := p.db.GetContext(ctx, &count, query, code)
	return count > 0, err
}

func (p *Postgres) GetStats(ctx context.Context) (totalFiles, activeFiles, totalReports int64, totalSize int64, err error) {
	err = p.db.GetContext(ctx, &totalFiles, `SELECT COUNT(*) FROM files`)
	if err != nil {
		return
	}

	err = p.db.GetContext(ctx, &activeFiles, `SELECT COUNT(*) FROM files WHERE is_deleted = FALSE AND expires_at > $1`, time.Now())
	if err != nil {
		return
	}

	err = p.db.GetContext(ctx, &totalReports, `SELECT COUNT(*) FROM reports`)
	if err != nil {
		return
	}

	err = p.db.GetContext(ctx, &totalSize, `SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE is_deleted = FALSE`)
	return
}

func (p *Postgres) CreateOrUpdateUserDevice(ctx context.Context, device *models.UserDevice) error {
	query := `
		INSERT INTO user_devices (
			id,
			cns_user_id,
			device_label,
			public_key_jwk,
			key_algorithm,
			key_version,
			created_at,
			last_seen_at,
			revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW(), NULL)
		ON CONFLICT (id) DO UPDATE
		SET device_label = EXCLUDED.device_label,
			public_key_jwk = EXCLUDED.public_key_jwk,
			key_algorithm = EXCLUDED.key_algorithm,
			key_version = EXCLUDED.key_version,
			last_seen_at = NOW(),
			revoked_at = NULL
		WHERE user_devices.cns_user_id = EXCLUDED.cns_user_id
	`
	res, err := p.db.ExecContext(ctx, query,
		device.ID,
		device.CNSUserID,
		device.DeviceLabel,
		device.PublicKeyJWK,
		device.KeyAlgorithm,
		device.KeyVersion,
	)
	if err != nil {
		return err
	}
	return requireDeviceUpsert(res)
}

// requireDeviceUpsert turns an upsert that touched no row into
// ErrDeviceIDConflict: device IDs are client-chosen, so an ID that already
// belongs to another account must never be re-assigned to the caller.
func requireDeviceUpsert(res sql.Result) error {
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return models.ErrDeviceIDConflict
	}
	return nil
}

func (p *Postgres) ResetTrustedDeviceState(ctx context.Context, device *models.UserDevice, envelope *models.UserKeyEnvelope) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE user_devices SET revoked_at = NOW() WHERE cns_user_id = $1 AND revoked_at IS NULL`, device.CNSUserID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_key_envelopes WHERE cns_user_id = $1`, device.CNSUserID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_identity_key_device_envelopes WHERE cns_user_id = $1`, device.CNSUserID); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Recovery revokes every device, so their open approval requests
	// (including the recovering device's own) are moot.
	if _, err := tx.ExecContext(ctx, `UPDATE device_enrollments SET status = $1 WHERE cns_user_id = $2 AND status = $3`,
		models.EnrollmentStatusExpired, device.CNSUserID, models.EnrollmentStatusPending); err != nil {
		_ = tx.Rollback()
		return err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO user_devices (
			id,
			cns_user_id,
			device_label,
			public_key_jwk,
			key_algorithm,
			key_version,
			created_at,
			last_seen_at,
			revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW(), NULL)
		ON CONFLICT (id) DO UPDATE
		SET device_label = EXCLUDED.device_label,
			public_key_jwk = EXCLUDED.public_key_jwk,
			key_algorithm = EXCLUDED.key_algorithm,
			key_version = EXCLUDED.key_version,
			last_seen_at = NOW(),
			revoked_at = NULL
		WHERE user_devices.cns_user_id = EXCLUDED.cns_user_id
	`, device.ID, device.CNSUserID, device.DeviceLabel, device.PublicKeyJWK, device.KeyAlgorithm, device.KeyVersion)
	if err == nil {
		err = requireDeviceUpsert(res)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_key_envelopes (
			id,
			cns_user_id,
			device_id,
			wrapped_user_key,
			uk_wrap_alg,
			uk_wrap_meta,
			key_version,
			created_at
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (cns_user_id, device_id) DO UPDATE
		SET wrapped_user_key = EXCLUDED.wrapped_user_key,
			uk_wrap_alg = EXCLUDED.uk_wrap_alg,
			uk_wrap_meta = EXCLUDED.uk_wrap_meta,
			key_version = EXCLUDED.key_version,
			created_at = NOW()
	`, envelope.CNSUserID, envelope.DeviceID, envelope.WrappedUserKey, envelope.UKWrapAlg, envelope.UKWrapMeta, envelope.KeyVersion); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (p *Postgres) GetActiveDevicesByUser(ctx context.Context, userID int64) ([]models.UserDevice, error) {
	query := `
		SELECT id, cns_user_id, device_label, public_key_jwk, key_algorithm, key_version, created_at, last_seen_at, revoked_at
		FROM user_devices
		WHERE cns_user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at ASC
	`
	var devices []models.UserDevice
	err := p.db.SelectContext(ctx, &devices, query, userID)
	return devices, err
}

func (p *Postgres) UpdateUserDeviceLabel(ctx context.Context, userID int64, deviceID, deviceLabel string) error {
	query := `
		UPDATE user_devices
		SET device_label = $1,
			last_seen_at = NOW()
		WHERE cns_user_id = $2
		  AND id = $3
		  AND revoked_at IS NULL
	`
	res, err := p.db.ExecContext(ctx, query, deviceLabel, userID, deviceID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return models.ErrDeviceNotFound
	}
	return nil
}

func (p *Postgres) SaveFileKeyEnvelope(ctx context.Context, envelope *models.FileKeyEnvelope) error {
	query := `
		INSERT INTO file_key_envelopes (file_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (file_id) DO UPDATE
		SET wrapped_dek = EXCLUDED.wrapped_dek,
			dek_wrap_alg = EXCLUDED.dek_wrap_alg,
			dek_wrap_nonce = EXCLUDED.dek_wrap_nonce,
			dek_wrap_version = EXCLUDED.dek_wrap_version
	`
	_, err := p.db.ExecContext(ctx, query,
		envelope.FileID,
		envelope.WrappedDEK,
		envelope.DEKWrapAlg,
		envelope.DEKWrapNonce,
		envelope.DEKWrapVersion,
	)
	return err
}

func (p *Postgres) GetOwnedRecentFiles(ctx context.Context, userID int64, searchQuery string, page, perPage int) ([]models.OwnedFileListItem, int, error) {
	offset := (page - 1) * perPage
	searchPattern := "%"
	if searchQuery != "" {
		searchPattern = "%" + searchQuery + "%"
	}

	countQuery := `
		SELECT COUNT(*)
		FROM files
		WHERE owner_cns_user_id = $1
		  AND is_deleted = FALSE
		  AND tunnel_id IS NULL
		  AND EXISTS (
			  SELECT 1 FROM file_key_envelopes fke
			  WHERE fke.file_id = files.id
		  )
		  AND ($2 = '%' OR original_name ILIKE $2)
	`

	var total int
	if err := p.db.GetContext(ctx, &total, countQuery, userID, searchPattern); err != nil {
		return nil, 0, err
	}

	itemsQuery := `
		SELECT
			id AS file_id,
			original_name AS filename,
			size_bytes,
			created_at,
			expires_at
		FROM files
		WHERE owner_cns_user_id = $1
		  AND is_deleted = FALSE
		  AND tunnel_id IS NULL
		  AND EXISTS (
			  SELECT 1 FROM file_key_envelopes fke
			  WHERE fke.file_id = files.id
		  )
		  AND ($2 = '%' OR original_name ILIKE $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`

	var items []models.OwnedFileListItem
	err := p.db.SelectContext(ctx, &items, itemsQuery, userID, searchPattern, perPage, offset)
	if err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

func (p *Postgres) GetOwnedFileWithEnvelope(ctx context.Context, userID int64, fileID string) (*models.File, *models.FileKeyEnvelope, error) {
	file := &models.File{}
	fileQuery := `
		SELECT *
		FROM files
		WHERE id = $1
		  AND owner_cns_user_id = $2
		  AND is_deleted = FALSE
	`
	if err := p.db.GetContext(ctx, file, fileQuery, fileID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	env := &models.FileKeyEnvelope{}
	envQuery := `
		SELECT file_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version, created_at
		FROM file_key_envelopes
		WHERE file_id = $1
	`
	if err := p.db.GetContext(ctx, env, envQuery, fileID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	if time.Now().After(file.ExpiresAt) {
		return nil, nil, models.ErrFileExpired
	}

	return file, env, nil
}

func (p *Postgres) GetTunnelRecipientFileWithEnvelope(ctx context.Context, userID int64, deviceID, fileID string) (*models.File, *models.FileKeyEnvelope, error) {
	file := &models.File{}
	fileQuery := `
		SELECT f.*
		FROM files f
		INNER JOIN tunnels t ON t.id = f.tunnel_id
		WHERE f.id = $1
		  AND f.is_deleted = FALSE
		  AND (
			t.initiator_cns_user_id = $2
			OR t.peer_cns_user_id = $2
		  )
	`
	if err := p.db.GetContext(ctx, file, fileQuery, fileID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	env := &models.FileKeyEnvelope{}
	envQuery := `
		SELECT file_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version, created_at
		FROM file_recipient_key_envelopes
		WHERE file_id = $1
		  AND recipient_cns_user_id = $2
		  AND recipient_device_id = $3
	`
	if err := p.db.GetContext(ctx, env, envQuery, fileID, userID, deviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	if time.Now().After(file.ExpiresAt) {
		return nil, nil, models.ErrFileExpired
	}

	return file, env, nil
}

func (p *Postgres) GetTunnelFileWithEnvelope(ctx context.Context, tunnelID, fileID string) (*models.File, *models.FileKeyEnvelope, error) {
	file := &models.File{}
	fileQuery := `
		SELECT f.*
		FROM files f
		WHERE f.id = $1
		  AND f.tunnel_id = $2
		  AND f.is_deleted = FALSE
	`
	if err := p.db.GetContext(ctx, file, fileQuery, fileID, tunnelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	if time.Now().After(file.ExpiresAt) {
		return nil, nil, models.ErrFileExpired
	}

	env := &models.FileKeyEnvelope{}
	envQuery := `
		SELECT file_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version, created_at
		FROM file_key_envelopes
		WHERE file_id = $1
		LIMIT 1
	`
	if err := p.db.GetContext(ctx, env, envQuery, fileID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, models.ErrFileNotFound
		}
		return nil, nil, err
	}

	return file, env, nil
}

func (p *Postgres) SaveUserKeyEnvelope(ctx context.Context, envelope *models.UserKeyEnvelope) error {
	query := `
		INSERT INTO user_key_envelopes (id, cns_user_id, device_id, wrapped_user_key, uk_wrap_alg, uk_wrap_meta, key_version)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
		ON CONFLICT (cns_user_id, device_id) DO UPDATE
		SET wrapped_user_key = EXCLUDED.wrapped_user_key,
			uk_wrap_alg = EXCLUDED.uk_wrap_alg,
			uk_wrap_meta = EXCLUDED.uk_wrap_meta,
			key_version = EXCLUDED.key_version,
			created_at = NOW()
	`
	_, err := p.db.ExecContext(ctx, query,
		envelope.CNSUserID,
		envelope.DeviceID,
		envelope.WrappedUserKey,
		envelope.UKWrapAlg,
		envelope.UKWrapMeta,
		envelope.KeyVersion,
	)
	return err
}

func (p *Postgres) GetUserKeyEnvelopeForDevice(ctx context.Context, userID int64, deviceID string) (*models.UserKeyEnvelope, error) {
	query := `
		SELECT id, cns_user_id, device_id, wrapped_user_key, uk_wrap_alg, uk_wrap_meta, key_version, created_at
		FROM user_key_envelopes
		WHERE cns_user_id = $1 AND device_id = $2
	`
	var env models.UserKeyEnvelope
	err := p.db.GetContext(ctx, &env, query, userID, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &env, nil
}

func (p *Postgres) CreateUserIdentityKey(ctx context.Context, key *models.UserIdentityKey) error {
	// Identity key versions are immutable; duplicate (user, version) inserts must fail
	// loudly rather than replacing key material that may already be in use.
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO user_identity_keys
			(cns_user_id, key_version, public_key_jwk, key_algorithm, status, created_at, activated_at, retired_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, key.CNSUserID, key.KeyVersion, key.PublicKeyJWK, key.KeyAlgorithm, key.Status,
		key.CreatedAt, key.ActivatedAt, key.RetiredAt)
	return err
}

func (p *Postgres) GetUserIdentityKey(ctx context.Context, userID int64, version int) (*models.UserIdentityKey, error) {
	var key models.UserIdentityKey
	err := p.db.GetContext(ctx, &key, `
		SELECT cns_user_id, key_version, public_key_jwk, key_algorithm, status,
			created_at, activated_at, retired_at
		FROM user_identity_keys
		WHERE cns_user_id = $1 AND key_version = $2
	`, userID, version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrIdentityKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (p *Postgres) GetActiveUserIdentityKey(ctx context.Context, userID int64) (*models.UserIdentityKey, error) {
	var key models.UserIdentityKey
	err := p.db.GetContext(ctx, &key, `
		SELECT cns_user_id, key_version, public_key_jwk, key_algorithm, status,
			created_at, activated_at, retired_at
		FROM user_identity_keys
		WHERE cns_user_id = $1 AND status = 'active'
		LIMIT 1
	`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrIdentityKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (p *Postgres) CreateUserIdentityKeyDeviceEnvelope(ctx context.Context, envelope *models.UserIdentityKeyDeviceEnvelope) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO user_identity_key_device_envelopes
			(id, cns_user_id, device_id, identity_key_version, wrapped_private_key, wrap_alg, wrap_meta, created_at)
		VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (cns_user_id, device_id, identity_key_version) DO UPDATE SET
			wrapped_private_key = EXCLUDED.wrapped_private_key,
			wrap_alg = EXCLUDED.wrap_alg,
			wrap_meta = EXCLUDED.wrap_meta,
			created_at = EXCLUDED.created_at
	`, envelope.ID, envelope.CNSUserID, envelope.DeviceID, envelope.IdentityKeyVersion,
		envelope.WrappedPrivateKey, envelope.WrapAlg, envelope.WrapMeta, envelope.CreatedAt)
	return err
}

func (p *Postgres) GetUserIdentityKeyDeviceEnvelope(ctx context.Context, userID int64, deviceID string, version int) (*models.UserIdentityKeyDeviceEnvelope, error) {
	var envelope models.UserIdentityKeyDeviceEnvelope
	err := p.db.GetContext(ctx, &envelope, `
		SELECT id, cns_user_id, device_id, identity_key_version, wrapped_private_key,
			wrap_alg, wrap_meta, created_at
		FROM user_identity_key_device_envelopes
		WHERE cns_user_id = $1 AND device_id = $2 AND identity_key_version = $3
	`, userID, deviceID, version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrDeviceEnvelopeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &envelope, nil
}

func (p *Postgres) DeleteUserIdentityKeyDeviceEnvelopesByUser(ctx context.Context, userID int64) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM user_identity_key_device_envelopes WHERE cns_user_id = $1`, userID)
	return err
}

func (p *Postgres) UpdateUserIdentityKeyPublicKey(ctx context.Context, userID int64, version int, publicKeyJWK json.RawMessage) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE user_identity_keys
		SET public_key_jwk = $1, created_at = NOW(), activated_at = NOW()
		WHERE cns_user_id = $2 AND key_version = $3
	`, publicKeyJWK, userID, version)
	return err
}

func (p *Postgres) CreateFileAccessKeyEnvelope(ctx context.Context, envelope *models.FileAccessKeyEnvelope) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO file_access_key_envelopes
			(file_id, recipient_cns_user_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce,
			 dek_wrap_version, recipient_key_version, access_kind, source_tunnel_id,
			 granted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (file_id, recipient_cns_user_id) DO UPDATE SET
			wrapped_dek = EXCLUDED.wrapped_dek,
			dek_wrap_alg = EXCLUDED.dek_wrap_alg,
			dek_wrap_nonce = EXCLUDED.dek_wrap_nonce,
			dek_wrap_version = EXCLUDED.dek_wrap_version,
			recipient_key_version = EXCLUDED.recipient_key_version,
			access_kind = EXCLUDED.access_kind,
			source_tunnel_id = EXCLUDED.source_tunnel_id,
			granted_at = EXCLUDED.granted_at,
			updated_at = EXCLUDED.updated_at
	`, envelope.FileID, envelope.RecipientCNSUserID, envelope.WrappedDEK,
		envelope.DEKWrapAlg, envelope.DEKWrapNonce, envelope.DEKWrapVersion,
		envelope.RecipientKeyVersion, envelope.AccessKind, envelope.SourceTunnelID,
		envelope.GrantedAt, envelope.CreatedAt, envelope.UpdatedAt)
	return err
}

// GetFileAccessKeyEnvelope returns the identity-wrapped DEK a user may use
// for a file: their own 'owner' envelope, or a transfer ('share') envelope
// once that transfer has been accepted. Pending transfers are withheld and
// declined ones have no envelope left, so both return ErrFileAccessNotFound.
func (p *Postgres) GetFileAccessKeyEnvelope(ctx context.Context, fileID string, recipientUserID int64) (*models.FileAccessKeyEnvelope, error) {
	var envelope models.FileAccessKeyEnvelope
	err := p.db.GetContext(ctx, &envelope, `
		SELECT e.file_id, e.recipient_cns_user_id, e.wrapped_dek, e.dek_wrap_alg, e.dek_wrap_nonce,
			e.dek_wrap_version, e.recipient_key_version, e.access_kind, e.source_tunnel_id,
			e.granted_at, e.created_at, e.updated_at
		FROM file_access_key_envelopes e
		WHERE e.file_id = $1 AND e.recipient_cns_user_id = $2
		  AND (e.access_kind = 'owner' OR EXISTS (
			SELECT 1 FROM file_transfers t
			WHERE t.file_id = e.file_id AND t.recipient_cns_user_id = e.recipient_cns_user_id
			  AND t.status = 'accepted'
		  ))
	`, fileID, recipientUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrFileAccessNotFound
	}
	if err != nil {
		return nil, err
	}
	return &envelope, nil
}

// GetRecentShareRecipients returns the users ownerUserID most recently sent
// transfers to, newest first, joined against the local user cache so callers
// can render a username and avatar. Recipients missing from the cache (or
// deactivated there) are skipped: there is nothing useful to display for them.
func (p *Postgres) GetRecentShareRecipients(ctx context.Context, ownerUserID int64, limit int) ([]models.User, error) {
	if limit <= 0 || limit > 50 {
		limit = 8
	}
	var users []models.User
	err := p.db.SelectContext(ctx, &users, `
		SELECT u.cns_user_id, u.username, u.avatar_url
		FROM (
			SELECT recipient_cns_user_id, MAX(created_at) AS last_shared_at
			FROM file_transfers
			WHERE sender_cns_user_id = $1
			GROUP BY recipient_cns_user_id
		) r
		JOIN users u ON u.cns_user_id = r.recipient_cns_user_id
		WHERE u.status = $2
		ORDER BY r.last_shared_at DESC
		LIMIT $3
	`, ownerUserID, models.UserStatusActive, limit)
	return users, err
}

func (p *Postgres) GetSharedWithMeFiles(ctx context.Context, userID int64, page, perPage int) ([]models.OwnedFileListItem, int, error) {
	offset := (page - 1) * perPage
	var total int
	if err := p.db.GetContext(ctx, &total, `
		SELECT COUNT(*)
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		WHERE t.recipient_cns_user_id = $1
		  AND t.status = 'accepted'
		  AND f.is_deleted = FALSE
		  AND f.expires_at > NOW()
	`, userID); err != nil {
		return nil, 0, err
	}

	var items []models.OwnedFileListItem
	if err := p.db.SelectContext(ctx, &items, `
		SELECT f.id AS file_id, f.original_name AS filename, f.size_bytes,
			f.created_at, f.expires_at
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		WHERE t.recipient_cns_user_id = $1
		  AND t.status = 'accepted'
		  AND f.is_deleted = FALSE
		  AND f.expires_at > NOW()
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, perPage, offset); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (p *Postgres) UserHasTrustedKeyEnvelope(ctx context.Context, userID int64) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM user_key_envelopes uke
			INNER JOIN user_devices ud ON ud.id = uke.device_id
			WHERE uke.cns_user_id = $1
			  AND ud.revoked_at IS NULL
		)
	`
	var hasTrusted bool
	err := p.db.GetContext(ctx, &hasTrusted, query, userID)
	if err != nil {
		return false, err
	}
	return hasTrusted, nil
}

func (p *Postgres) CreateEnrollmentRequest(ctx context.Context, enrollment *models.DeviceEnrollment) error {
	query := `
		INSERT INTO device_enrollments (
			id,
			cns_user_id,
			request_device_id,
			verification_code,
			status,
			expires_at,
			created_at
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NOW())
		RETURNING id, created_at
	`
	err := p.db.QueryRowContext(ctx, query,
		enrollment.CNSUserID,
		enrollment.RequestDeviceID,
		enrollment.VerificationCode,
		enrollment.Status,
		enrollment.ExpiresAt,
	).Scan(&enrollment.ID, &enrollment.CreatedAt)
	return err
}

func (p *Postgres) ListPendingEnrollments(ctx context.Context, userID int64) ([]models.DeviceEnrollment, error) {
	query := `
		SELECT id, cns_user_id, request_device_id, verification_code, status, approved_by_device_id, expires_at, created_at, approved_at
		FROM device_enrollments
		WHERE cns_user_id = $1
		  AND status = $2
		  AND expires_at > NOW()
		  -- A device that is already trusted (e.g. it recovered the account
		  -- after asking for approval) has nothing left to approve.
		  AND NOT EXISTS (
			SELECT 1 FROM user_key_envelopes uke
			WHERE uke.cns_user_id = device_enrollments.cns_user_id
			  AND uke.device_id = device_enrollments.request_device_id
		  )
		ORDER BY created_at DESC
	`
	items := []models.DeviceEnrollment{}
	err := p.db.SelectContext(ctx, &items, query, userID, models.EnrollmentStatusPending)
	return items, err
}

func (p *Postgres) GetEnrollmentByID(ctx context.Context, userID int64, enrollmentID string) (*models.DeviceEnrollment, error) {
	query := `
		SELECT id, cns_user_id, request_device_id, verification_code, status, approved_by_device_id, expires_at, created_at, approved_at
		FROM device_enrollments
		WHERE id = $1 AND cns_user_id = $2
	`
	var item models.DeviceEnrollment
	err := p.db.GetContext(ctx, &item, query, enrollmentID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (p *Postgres) GetPendingEnrollmentForDevice(ctx context.Context, userID int64, deviceID string) (*models.DeviceEnrollment, error) {
	query := `
		SELECT id, cns_user_id, request_device_id, verification_code, status, approved_by_device_id, expires_at, created_at, approved_at
		FROM device_enrollments
		WHERE cns_user_id = $1
		  AND request_device_id = $2
		  AND status = $3
		  AND expires_at > NOW()
		ORDER BY created_at DESC
		LIMIT 1
	`
	var item models.DeviceEnrollment
	err := p.db.GetContext(ctx, &item, query, userID, deviceID, models.EnrollmentStatusPending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (p *Postgres) ApproveEnrollment(ctx context.Context, userID int64, enrollmentID, approverDeviceID string) error {
	query := `
		UPDATE device_enrollments
		SET status = $1,
			approved_by_device_id = $2,
			approved_at = NOW()
		WHERE id = $3
		  AND cns_user_id = $4
		  AND status = $5
		  AND expires_at > NOW()
	`
	res, err := p.db.ExecContext(ctx, query,
		models.EnrollmentStatusApproved,
		approverDeviceID,
		enrollmentID,
		userID,
		models.EnrollmentStatusPending,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return models.ErrFileNotFound
	}
	return nil
}

func (p *Postgres) RejectEnrollment(ctx context.Context, userID int64, enrollmentID string) error {
	query := `
		UPDATE device_enrollments
		SET status = $1
		WHERE id = $2
		  AND cns_user_id = $3
		  AND status = $4
		  AND expires_at > NOW()
	`
	res, err := p.db.ExecContext(ctx, query,
		models.EnrollmentStatusRejected,
		enrollmentID,
		userID,
		models.EnrollmentStatusPending,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return models.ErrUploadNotPending
	}
	return nil
}

func (p *Postgres) TouchExpiredEnrollments(ctx context.Context, userID int64) error {
	query := `
		UPDATE device_enrollments
		SET status = $1
		WHERE cns_user_id = $2
		  AND status = $3
		  AND expires_at <= NOW()
	`
	_, err := p.db.ExecContext(ctx, query, models.EnrollmentStatusExpired, userID, models.EnrollmentStatusPending)
	return err
}

// GetUser returns the local cache row for a CNS user, or ErrUserNotFound if
// this service has never provisioned/synced them.
func (p *Postgres) GetUser(ctx context.Context, cnsUserID int64) (*models.User, error) {
	var user models.User
	err := p.db.GetContext(ctx, &user, `
		SELECT cns_user_id, username, avatar_url, status, created_at, last_synced_at, deactivated_at
		FROM users
		WHERE cns_user_id = $1
	`, cnsUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UpsertUser creates or refreshes the local cache row for a CNS user. Status
// transitions back to active and deactivated_at is cleared whenever a sync
// succeeds, since a successful CNS profile fetch means the user is provisioned.
func (p *Postgres) UpsertUser(ctx context.Context, user *models.User) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO users (cns_user_id, username, avatar_url, status, created_at, last_synced_at, deactivated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW(), NULL)
		ON CONFLICT (cns_user_id) DO UPDATE SET
			username = EXCLUDED.username,
			avatar_url = EXCLUDED.avatar_url,
			status = EXCLUDED.status,
			last_synced_at = NOW(),
			deactivated_at = NULL
	`, user.CNSUserID, user.Username, user.AvatarURL, user.Status)
	return err
}

// likeEscaper escapes LIKE metacharacters so user input matches literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchUsersByUsername finds active local users whose username contains
// query (case-insensitive), for the in-app share-recipient picker. This
// reads Sendly's own cache rather than CNS, since CNS has no username
// search endpoint for services to call.
func (p *Postgres) SearchUsersByUsername(ctx context.Context, query string, limit int) ([]models.User, error) {
	var users []models.User
	err := p.db.SelectContext(ctx, &users, `
		SELECT cns_user_id, username, avatar_url
		FROM users
		WHERE status = $1 AND username ILIKE $2 ESCAPE '\'
		ORDER BY username
		LIMIT $3
	`, models.UserStatusActive, "%"+likeEscaper.Replace(query)+"%", limit)
	if err != nil {
		return nil, err
	}
	return users, nil
}

// DeactivateStaleUsers marks locally-cached users inactive if they haven't
// synced with CNS since staleBefore. This is the reconciliation fallback:
// CNS has no webhooks and no service-scoped "check user X" call, so there is
// no way to proactively re-verify a user without their own bearer token. A
// user who goes quiet simply ages out locally, and flips back to active the
// next time they make an authenticated request and resync.
func (p *Postgres) DeactivateStaleUsers(ctx context.Context, staleBefore time.Time) (int64, error) {
	result, err := p.db.ExecContext(ctx, `
		UPDATE users
		SET status = $1, deactivated_at = NOW()
		WHERE status = $2 AND last_synced_at < $3
	`, models.UserStatusInactive, models.UserStatusActive, staleBefore)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
