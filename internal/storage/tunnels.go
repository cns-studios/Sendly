package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"shareit/internal/models"
)

func (p *Postgres) CreateTunnel(ctx context.Context, tunnel *models.Tunnel) error {
	query := `
		INSERT INTO tunnels (
			id,
			code,
			initiator_cns_user_id,
			initiator_device_id,
			peer_cns_user_id,
			peer_device_id,
			duration_minutes,
			status,
			initiator_confirmed,
			peer_confirmed,
			expires_at,
			created_at,
			confirmed_at,
			ended_at,
			ended_by_cns_user_id,
			ended_by_device_id
		)
		VALUES (
			gen_random_uuid(),
			$1,
			$2,
			$3,
			NULL,
			NULL,
			$4,
			$5,
			$6,
			$7,
			$8,
			NOW(),
			NULL,
			NULL,
			NULL,
			NULL
		)
		RETURNING id, created_at
	`
	tunnel.Status = models.TunnelStatusPending
	tunnel.InitiatorConfirmed = false
	tunnel.PeerConfirmed = false
	return p.db.QueryRowContext(ctx, query,
		tunnel.Code,
		tunnel.InitiatorCNSUserID,
		tunnel.InitiatorDeviceID,
		tunnel.DurationMinutes,
		tunnel.Status,
		tunnel.InitiatorConfirmed,
		tunnel.PeerConfirmed,
		tunnel.ExpiresAt,
	).Scan(&tunnel.ID, &tunnel.CreatedAt)
}

func (p *Postgres) GetTunnelByID(ctx context.Context, tunnelID string) (*models.Tunnel, error) {
	var tunnel models.Tunnel
	query := `SELECT * FROM tunnels WHERE id = $1`
	err := p.db.GetContext(ctx, &tunnel, query, tunnelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(tunnel.Status, models.TunnelStatusEnded) || strings.EqualFold(tunnel.Status, models.TunnelStatusExpired) {
		return &tunnel, models.ErrFileExpired
	}
	if time.Now().After(tunnel.ExpiresAt) {
		return &tunnel, models.ErrFileExpired
	}
	return &tunnel, nil
}

func (p *Postgres) GetTunnelByCode(ctx context.Context, code string) (*models.Tunnel, error) {
	var tunnel models.Tunnel
	query := `SELECT * FROM tunnels WHERE code = $1`
	err := p.db.GetContext(ctx, &tunnel, query, code)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrInvalidCode
	}
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(tunnel.Status, models.TunnelStatusEnded) || strings.EqualFold(tunnel.Status, models.TunnelStatusExpired) {
		return &tunnel, models.ErrFileExpired
	}
	if time.Now().After(tunnel.ExpiresAt) {
		return &tunnel, models.ErrFileExpired
	}
	return &tunnel, nil
}

func (p *Postgres) GetTunnelFiles(ctx context.Context, tunnelID string) ([]models.TunnelFileListItem, error) {
	var files []models.TunnelFileListItem
	query := `
		SELECT id AS file_id, original_name AS filename, size_bytes, created_at, expires_at
		FROM files
		WHERE tunnel_id = $1
		  AND is_deleted = FALSE
		ORDER BY created_at DESC
	`
	err := p.db.SelectContext(ctx, &files, query, tunnelID)
	return files, err
}

func (p *Postgres) GetTunnelFileIDs(ctx context.Context, tunnelID string) ([]string, error) {
	var fileIDs []string
	query := `SELECT id FROM files WHERE tunnel_id = $1 AND is_deleted = FALSE`
	err := p.db.SelectContext(ctx, &fileIDs, query, tunnelID)
	return fileIDs, err
}

func (p *Postgres) JoinTunnel(ctx context.Context, tunnelID string, userID int64, deviceID string) (*models.Tunnel, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}

	var tunnel models.Tunnel
	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1 FOR UPDATE`, tunnelID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrFileNotFound
		}
		return nil, err
	}

	if time.Now().After(tunnel.ExpiresAt) || strings.EqualFold(tunnel.Status, models.TunnelStatusEnded) || strings.EqualFold(tunnel.Status, models.TunnelStatusExpired) {
		_ = tx.Rollback()
		return nil, models.ErrFileExpired
	}

	if tunnel.PeerCNSUserID.Valid {
		_ = tx.Rollback()
		return nil, models.ErrFileNotFound
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE tunnels
		SET peer_cns_user_id = $1,
			peer_device_id = $2,
			peer_confirmed = TRUE,
			status = CASE WHEN initiator_confirmed THEN $3 ELSE $4 END,
			confirmed_at = CASE WHEN initiator_confirmed THEN NOW() ELSE confirmed_at END
		WHERE id = $5
	`, userID, nullableString(deviceID), models.TunnelStatusActive, models.TunnelStatusJoined, tunnelID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1`, tunnelID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	return &tunnel, tx.Commit()
}

func (p *Postgres) ConfirmTunnel(ctx context.Context, tunnelID string, userID int64, deviceID string) (*models.Tunnel, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}

	var tunnel models.Tunnel
	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1 FOR UPDATE`, tunnelID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrFileNotFound
		}
		return nil, err
	}

	if time.Now().After(tunnel.ExpiresAt) || strings.EqualFold(tunnel.Status, models.TunnelStatusEnded) || strings.EqualFold(tunnel.Status, models.TunnelStatusExpired) {
		_ = tx.Rollback()
		return nil, models.ErrFileExpired
	}

	isInitiatorActor := tunnel.InitiatorCNSUserID == userID || (userID == 0 && tunnel.InitiatorDeviceID.Valid && tunnel.InitiatorDeviceID.String == deviceID)
	isPeerActor := (tunnel.PeerCNSUserID.Valid && tunnel.PeerCNSUserID.Int64 == userID) || (userID == 0 && tunnel.PeerDeviceID.Valid && tunnel.PeerDeviceID.String == deviceID)
	setInitiator := isInitiatorActor && !tunnel.InitiatorConfirmed
	setPeer := isPeerActor && !tunnel.PeerConfirmed
	if !isInitiatorActor && !isPeerActor {
		_ = tx.Rollback()
		return nil, models.ErrFileNotFound
	}

	if !setInitiator && !setPeer {
		return &tunnel, tx.Commit()
	}

	updates := []string{}
	args := []any{}
	idx := 1
	if setInitiator {
		updates = append(updates, fmt.Sprintf("initiator_confirmed = $%d", idx))
		args = append(args, true)
		idx++
	}
	if setPeer {
		updates = append(updates, fmt.Sprintf("peer_confirmed = $%d", idx))
		args = append(args, true)
		idx++
	}
	if tunnel.InitiatorConfirmed || setInitiator {
		if tunnel.PeerConfirmed || setPeer {
			updates = append(updates, fmt.Sprintf("status = $%d", idx))
			args = append(args, models.TunnelStatusActive)
			idx++
			updates = append(updates, fmt.Sprintf("confirmed_at = NOW()"))
		} else {
			updates = append(updates, fmt.Sprintf("status = $%d", idx))
			args = append(args, models.TunnelStatusJoined)
			idx++
		}
	} else if setPeer {
		updates = append(updates, fmt.Sprintf("status = $%d", idx))
		args = append(args, models.TunnelStatusJoined)
		idx++
	}

	query := fmt.Sprintf(`UPDATE tunnels SET %s WHERE id = $%d`, strings.Join(updates, ", "), idx)
	args = append(args, tunnelID)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1`, tunnelID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	return &tunnel, tx.Commit()
}

func (p *Postgres) EndTunnel(ctx context.Context, tunnelID string, userID int64, deviceID string) error {
	query := `
		UPDATE tunnels
		SET status = $1,
			ended_at = NOW(),
			ended_by_cns_user_id = $2,
			ended_by_device_id = $3
		WHERE id = $4
		  AND status <> $5
	`
	res, err := p.db.ExecContext(ctx, query, models.TunnelStatusEnded, userID, nullableString(deviceID), tunnelID, models.TunnelStatusEnded)
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

func (p *Postgres) DeleteTunnel(ctx context.Context, tunnelID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM tunnels WHERE id = $1`, tunnelID)
	return err
}

func (p *Postgres) TunnelBelongsToUser(ctx context.Context, tunnelID string, userID int64) (bool, error) {
	var count int
	query := `
		SELECT COUNT(*)
		FROM tunnels
		WHERE id = $1
		  AND (
			initiator_cns_user_id = $2
			OR peer_cns_user_id = $2
		  )
	`
	err := p.db.GetContext(ctx, &count, query, tunnelID, userID)
	return count > 0, err
}

func (p *Postgres) TunnelCodeExists(ctx context.Context, code string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM tunnels WHERE code = $1`
	err := p.db.GetContext(ctx, &count, query, code)
	return count > 0, err
}

func nullableString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}