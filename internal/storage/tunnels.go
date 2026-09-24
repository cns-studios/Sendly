package storage

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"sendly/internal/models"
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
			ended_by_device_id,
			host_token
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
			NULL,
			$9
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
		tunnel.HostToken,
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

// JoinTunnel adds the caller as a participant. A device ID that already has a
// participant row may only be re-joined by that row's owner (same CNS user, or
// an anonymous caller presenting the row's participant token); anyone else
// gets ErrParticipantConflict, so a joiner can never replace another
// participant's public key. It reports whether join.NewTokenHash was stored,
// i.e. whether a new anonymous participant was created.
func (p *Postgres) JoinTunnel(ctx context.Context, tunnelID string, join models.TunnelJoin) (*models.Tunnel, bool, error) {
	userID, deviceID := join.UserID, strings.TrimSpace(join.DeviceID)
	if userID == 0 && deviceID == "" {
		return nil, false, models.ErrGuestDeviceRequired
	}

	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}

	var tunnel models.Tunnel
	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1 FOR UPDATE`, tunnelID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, models.ErrFileNotFound
		}
		return nil, false, err
	}

	if time.Now().After(tunnel.ExpiresAt) || strings.EqualFold(tunnel.Status, models.TunnelStatusEnded) || strings.EqualFold(tunnel.Status, models.TunnelStatusExpired) {
		_ = tx.Rollback()
		return nil, false, models.ErrFileExpired
	}

	var existing models.TunnelParticipant
	var found bool
	if deviceID != "" {
		err = tx.GetContext(ctx, &existing, `
			SELECT id, tunnel_id, cns_user_id, device_id, joined_at,
				COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
				key_algorithm, key_version, participant_token_hash, approved_at
			FROM tunnel_participants
			WHERE tunnel_id = $1 AND device_id::text = $2
			FOR UPDATE
		`, tunnelID, deviceID)
	} else {
		err = tx.GetContext(ctx, &existing, `
			SELECT id, tunnel_id, cns_user_id, device_id, joined_at,
				COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
				key_algorithm, key_version, participant_token_hash, approved_at
			FROM tunnel_participants
			WHERE tunnel_id = $1 AND cns_user_id = $2 AND device_id IS NULL
			FOR UPDATE
		`, tunnelID, userID)
	}
	switch {
	case err == nil:
		found = true
	case errors.Is(err, sql.ErrNoRows):
	default:
		_ = tx.Rollback()
		return nil, false, err
	}

	issued := false
	if found {
		if !participantOwnedBy(existing, userID, join.PresentedTokenHash) {
			_ = tx.Rollback()
			return nil, false, models.ErrParticipantConflict
		}
	} else {
		var tokenHash sql.NullString
		if userID == 0 {
			tokenHash = nullableString(join.NewTokenHash)
			issued = tokenHash.Valid
		}
		if err := tx.GetContext(ctx, &existing.ID, `
			INSERT INTO tunnel_participants (tunnel_id, cns_user_id, device_id, participant_token_hash)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, tunnelID, nullableInt64(userID), nullableString(deviceID), tokenHash); err != nil {
			_ = tx.Rollback()
			return nil, false, err
		}
	}

	if len(join.PublicKeyJWK) > 0 && deviceID != "" && !bytes.Equal(existing.PublicKeyJWK, join.PublicKeyJWK) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tunnel_participants
			SET public_key_jwk = $1, key_algorithm = $2, key_version = $3
			WHERE id = $4
		`, join.PublicKeyJWK, join.KeyAlgorithm, join.KeyVersion, existing.ID); err != nil {
			_ = tx.Rollback()
			return nil, false, err
		}
		// An envelope wrapped for the previous key is useless now; drop it so
		// the host wraps the session key for the new one.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM tunnel_participant_envelopes
			WHERE tunnel_id = $1 AND participant_device_id = $2
		`, tunnelID, deviceID); err != nil {
			_ = tx.Rollback()
			return nil, false, err
		}
	}

	if !tunnel.PeerCNSUserID.Valid && userID != 0 {
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
			return nil, false, err
		}
	} else if !tunnel.PeerCNSUserID.Valid && userID == 0 && deviceID != "" {
		_, err = tx.ExecContext(ctx, `
			UPDATE tunnels
			SET peer_device_id = $1,
				peer_confirmed = TRUE,
				status = CASE WHEN initiator_confirmed THEN $2 ELSE $3 END,
				confirmed_at = CASE WHEN initiator_confirmed THEN NOW() ELSE confirmed_at END
			WHERE id = $4
		`, nullableString(deviceID), models.TunnelStatusActive, models.TunnelStatusJoined, tunnelID)
		if err != nil {
			_ = tx.Rollback()
			return nil, false, err
		}
	}

	if err := tx.GetContext(ctx, &tunnel, `SELECT * FROM tunnels WHERE id = $1`, tunnelID); err != nil {
		_ = tx.Rollback()
		return nil, false, err
	}

	return &tunnel, issued, tx.Commit()
}

// participantOwnedBy reports whether the caller owns a participant row: a
// signed-in caller by CNS user, an anonymous caller by the participant token.
func participantOwnedBy(participant models.TunnelParticipant, userID int64, presentedTokenHash string) bool {
	if userID != 0 {
		return participant.CNSUserID.Valid && participant.CNSUserID.Int64 == userID
	}
	return !participant.CNSUserID.Valid && participant.TokenHash.Valid && presentedTokenHash != "" &&
		subtle.ConstantTimeCompare([]byte(participant.TokenHash.String), []byte(presentedTokenHash)) == 1
}

// FindTunnelParticipant returns the participant row owned by the caller, or
// nil: for a signed-in caller the row with their CNS user (preferring the
// given device), for an anonymous caller the row for deviceID whose token
// matches presentedTokenHash.
func (p *Postgres) FindTunnelParticipant(ctx context.Context, tunnelID string, userID int64, deviceID, presentedTokenHash string) (*models.TunnelParticipant, error) {
	var participants []models.TunnelParticipant
	var err error
	const columns = `id, tunnel_id, cns_user_id, device_id, joined_at,
		COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
		key_algorithm, key_version, participant_token_hash, approved_at`
	if userID != 0 {
		err = p.db.SelectContext(ctx, &participants, `
			SELECT `+columns+`
			FROM tunnel_participants
			WHERE tunnel_id = $1 AND cns_user_id = $2
			ORDER BY (device_id::text = $3) DESC NULLS LAST, joined_at ASC
		`, tunnelID, userID, strings.TrimSpace(deviceID))
	} else {
		if strings.TrimSpace(deviceID) == "" || presentedTokenHash == "" {
			return nil, nil
		}
		err = p.db.SelectContext(ctx, &participants, `
			SELECT `+columns+`
			FROM tunnel_participants
			WHERE tunnel_id = $1 AND device_id::text = $2 AND cns_user_id IS NULL
		`, tunnelID, strings.TrimSpace(deviceID))
	}
	if err != nil {
		return nil, err
	}
	markApproved(participants)
	for i := range participants {
		if participantOwnedBy(participants[i], userID, presentedTokenHash) {
			return &participants[i], nil
		}
	}
	return nil, nil
}

func markApproved(participants []models.TunnelParticipant) {
	for i := range participants {
		participants[i].Approved = participants[i].ApprovedAt.Valid
	}
}

// GetTunnelParticipantByDevice returns the participant row for a device, or
// nil if there is none.
func (p *Postgres) GetTunnelParticipantByDevice(ctx context.Context, tunnelID, deviceID string) (*models.TunnelParticipant, error) {
	var participant models.TunnelParticipant
	err := p.db.GetContext(ctx, &participant, `
		SELECT id, tunnel_id, cns_user_id, device_id, joined_at,
			COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
			key_algorithm, key_version, participant_token_hash, approved_at
		FROM tunnel_participants
		WHERE tunnel_id = $1 AND device_id::text = $2
		ORDER BY joined_at ASC
		LIMIT 1
	`, tunnelID, strings.TrimSpace(deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	participant.Approved = participant.ApprovedAt.Valid
	return &participant, nil
}

// ApproveTunnelParticipant marks a participant approved by the host.
func (p *Postgres) ApproveTunnelParticipant(ctx context.Context, tunnelID, participantID string) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE tunnel_participants
		SET approved_at = COALESCE(approved_at, NOW())
		WHERE tunnel_id = $1 AND id::text = $2
	`, tunnelID, participantID)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return models.ErrFileNotFound
	}
	return nil
}

// RejectTunnelParticipant removes a participant, its key envelope, and, if it
// was the tunnel's peer, the peer assignment (so no file keys get wrapped for
// it). It returns the removed row.
func (p *Postgres) RejectTunnelParticipant(ctx context.Context, tunnelID, participantID string) (*models.TunnelParticipant, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var participant models.TunnelParticipant
	err = tx.GetContext(ctx, &participant, `
		DELETE FROM tunnel_participants
		WHERE tunnel_id = $1 AND id::text = $2
		RETURNING id, tunnel_id, cns_user_id, device_id, joined_at,
			COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
			key_algorithm, key_version, participant_token_hash, approved_at
	`, tunnelID, participantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	if participant.DeviceID.Valid {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM tunnel_participant_envelopes
			WHERE tunnel_id = $1 AND participant_device_id = $2
		`, tunnelID, participant.DeviceID.String); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tunnels
		SET peer_cns_user_id = NULL, peer_device_id = NULL, peer_confirmed = FALSE
		WHERE id = $1
		  AND (($2::bigint IS NOT NULL AND peer_cns_user_id = $2::bigint)
		    OR ($3::text IS NOT NULL AND peer_device_id::text = $3::text))
	`, tunnelID, participant.CNSUserID, participant.DeviceID); err != nil {
		return nil, err
	}
	return &participant, tx.Commit()
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

// AddTunnelParticipant adds the host's own participant row, which is approved
// from the start.
func (p *Postgres) AddTunnelParticipant(ctx context.Context, tunnelID string, userID int64, deviceID string) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO tunnel_participants (tunnel_id, cns_user_id, device_id, approved_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT DO NOTHING
	`, tunnelID, nullableInt64(userID), nullableString(deviceID))
	return err
}

func (p *Postgres) GetTunnelParticipants(ctx context.Context, tunnelID string) ([]models.TunnelParticipant, error) {
	var participants []models.TunnelParticipant
	query := `
		SELECT
			id,
			tunnel_id,
			cns_user_id,
			device_id,
			joined_at,
			COALESCE(public_key_jwk, 'null'::jsonb) AS public_key_jwk,
			COALESCE(key_algorithm, '') AS key_algorithm,
			COALESCE(key_version, 0) AS key_version,
			approved_at
		FROM tunnel_participants
		WHERE tunnel_id = $1
		ORDER BY joined_at ASC
	`
	err := p.db.SelectContext(ctx, &participants, query, tunnelID)
	if err != nil {
		return participants, err
	}
	markApproved(participants)
	return participants, nil
}

func (p *Postgres) RemoveTunnelParticipant(ctx context.Context, tunnelID string, userID int64, deviceID string) error {
	if userID != 0 {
		_, err := p.db.ExecContext(ctx, `DELETE FROM tunnel_participants WHERE tunnel_id = $1 AND cns_user_id = $2`, tunnelID, userID)
		return err
	}
	_, err := p.db.ExecContext(ctx, `DELETE FROM tunnel_participants WHERE tunnel_id = $1 AND device_id = $2`, tunnelID, deviceID)
	return err
}

func (p *Postgres) CountTunnelParticipants(ctx context.Context, tunnelID string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM tunnel_participants WHERE tunnel_id = $1`
	err := p.db.GetContext(ctx, &count, query, tunnelID)
	return count, err
}

func nullableInt64(value int64) sql.NullInt64 {
	if value == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullableString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}





func (p *Postgres) GetParticipantsWithPublicKeys(ctx context.Context, tunnelID string) ([]models.TunnelParticipant, error) {
	var participants []models.TunnelParticipant
	err := p.db.SelectContext(ctx, &participants, `
		SELECT * FROM tunnel_participants
		WHERE  tunnel_id      = $1
		  AND  public_key_jwk IS NOT NULL
		ORDER BY joined_at ASC
	`, tunnelID)
	markApproved(participants)
	return participants, err
}



func (p *Postgres) SaveTunnelParticipantEnvelope(ctx context.Context, tunnelID, participantDeviceID string, wrappedDEK, nonce []byte, wrapAlg string, wrapVersion int) error {
	res, err := p.db.ExecContext(ctx, `
		INSERT INTO tunnel_participant_envelopes
			(tunnel_id, participant_device_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tunnel_id, participant_device_id) DO NOTHING
	`, tunnelID, participantDeviceID, wrappedDEK, wrapAlg, nonce, wrapVersion)
	if err != nil {
		return err
	}
	// An existing envelope is never replaced: whoever could overwrite it
	// could hand the participant a key of their own choosing.
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return models.ErrEnvelopeExists
	}
	return nil
}


func (p *Postgres) GetTunnelParticipantEnvelope(ctx context.Context, tunnelID, deviceID string) (*models.TunnelGuestEnvelope, error) {
	var row struct {
		WrappedDEK     []byte `db:"wrapped_dek"`
		DEKWrapAlg     string `db:"dek_wrap_alg"`
		DEKWrapNonce   []byte `db:"dek_wrap_nonce"`
		DEKWrapVersion int    `db:"dek_wrap_version"`
	}
	err := p.db.GetContext(ctx, &row, `
		SELECT wrapped_dek, dek_wrap_alg, dek_wrap_nonce, dek_wrap_version
		FROM   tunnel_participant_envelopes
		WHERE  tunnel_id             = $1
		  AND  participant_device_id = $2
	`, tunnelID, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil 
	}
	if err != nil {
		return nil, err
	}
	return &models.TunnelGuestEnvelope{
		WrappedDEKB64:   base64.StdEncoding.EncodeToString(row.WrappedDEK),
		DEKWrapAlg:      row.DEKWrapAlg,
		DEKWrapNonceB64: base64.StdEncoding.EncodeToString(row.DEKWrapNonce),
		DEKWrapVersion:  row.DEKWrapVersion,
	}, nil
}



func (p *Postgres) ParticipantHasEnvelope(ctx context.Context, tunnelID, deviceID string) (bool, error) {
	var count int
	err := p.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM tunnel_participant_envelopes
		WHERE tunnel_id = $1 AND participant_device_id = $2
	`, tunnelID, deviceID)
	return count > 0, err
}