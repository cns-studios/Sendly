package storage

import (
	"context"
	"database/sql"
	"errors"

	"sendly/internal/models"
)

// Legacy user key: every account used to have one AES "user key", wrapped
// per device in user_key_envelopes, that wrapped all of its file keys. The
// identity key replaced it. These queries remain only so accounts from
// before the switch can be migrated; delete this file together with the
// identity migration.

// UserHasLegacyUserKey reports whether a non-revoked device of the user still
// holds a legacy user key envelope, i.e. the account predates identity keys.
func (p *Postgres) UserHasLegacyUserKey(ctx context.Context, userID int64) (bool, error) {
	var has bool
	err := p.db.GetContext(ctx, &has, `
		SELECT EXISTS (
			SELECT 1
			FROM user_key_envelopes uke
			JOIN user_devices ud ON ud.id = uke.device_id
			WHERE uke.cns_user_id = $1
			  AND ud.revoked_at IS NULL
		)
	`, userID)
	return has, err
}

// GetLegacyUserKeyEnvelope returns a non-revoked device's copy of the user's
// legacy user key.
func (p *Postgres) GetLegacyUserKeyEnvelope(ctx context.Context, userID int64, deviceID string) (*models.UserKeyEnvelope, error) {
	var envelope models.UserKeyEnvelope
	err := p.db.GetContext(ctx, &envelope, `
		SELECT uke.id, uke.cns_user_id, uke.device_id, uke.wrapped_user_key, uke.uk_wrap_alg,
			uke.uk_wrap_meta, uke.key_version, uke.created_at
		FROM user_key_envelopes uke
		JOIN user_devices ud ON ud.id = uke.device_id AND ud.cns_user_id = uke.cns_user_id
		WHERE uke.cns_user_id = $1 AND uke.device_id::text = $2 AND ud.revoked_at IS NULL
	`, userID, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrDeviceEnvelopeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &envelope, nil
}

// StartIdentityMigration creates a legacy account's identity key (version 1)
// with the migrating device's copy and the escrow for its other devices. It
// fails if the account already has an identity key.
func (p *Postgres) StartIdentityMigration(ctx context.Context, idKey *models.UserIdentityKey, envelope *models.UserIdentityKeyDeviceEnvelope, escrow *models.LegacyIdentityEscrow) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertIdentityKeyWithEnvelope(ctx, tx, idKey, envelope); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_identity_escrow
			(cns_user_id, identity_key_version, wrapped_private_key, wrap_nonce, wrap_alg)
		VALUES ($1, $2, $3, $4, $5)
	`, escrow.CNSUserID, escrow.IdentityKeyVersion, escrow.WrappedPrivateKey, escrow.WrapNonce, escrow.WrapAlg); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) GetLegacyIdentityEscrow(ctx context.Context, userID int64) (*models.LegacyIdentityEscrow, error) {
	var escrow models.LegacyIdentityEscrow
	err := p.db.GetContext(ctx, &escrow, `
		SELECT cns_user_id, identity_key_version, wrapped_private_key, wrap_nonce, wrap_alg, created_at
		FROM legacy_identity_escrow
		WHERE cns_user_id = $1
	`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrIdentityKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &escrow, nil
}

// legacyFilesToMigrate selects the user's live, non-tunnel uploads whose key
// is only wrapped with the legacy user key.
const legacyFilesToMigrate = `
	FROM files f
	JOIN file_key_envelopes fke ON fke.file_id = f.id
	WHERE f.owner_cns_user_id = $1
	  AND f.is_deleted = FALSE
	  AND f.expires_at > NOW()
	  AND f.tunnel_id IS NULL
	  AND fke.dek_wrap_alg = 'AES-GCM-UK-v1'
	  AND NOT EXISTS (
		SELECT 1 FROM file_access_key_envelopes fae
		WHERE fae.file_id = f.id AND fae.recipient_cns_user_id = f.owner_cns_user_id
	  )
`

// ListLegacyFilesToMigrate returns up to limit such files with an id after
// the given one, and how many remain after them.
func (p *Postgres) ListLegacyFilesToMigrate(ctx context.Context, userID int64, after string, limit int) ([]models.LegacyFileKey, int, error) {
	items := []models.LegacyFileKey{}
	if err := p.db.SelectContext(ctx, &items, `
		SELECT f.id AS file_id, fke.wrapped_dek, fke.dek_wrap_alg, fke.dek_wrap_nonce
	`+legacyFilesToMigrate+`
		  AND f.id > $2
		ORDER BY f.id
		LIMIT $3
	`, userID, after, limit); err != nil {
		return nil, 0, err
	}
	last := after
	if len(items) > 0 {
		last = items[len(items)-1].FileID
	}
	var remaining int
	err := p.db.GetContext(ctx, &remaining, `SELECT COUNT(*) `+legacyFilesToMigrate+` AND f.id > $2`, userID, last)
	return items, remaining, err
}

// StoreMigratedOwnerEnvelopes stores owner envelopes re-wrapped for identity
// key version, only for the user's own files that have none yet. It returns
// how many were stored.
func (p *Postgres) StoreMigratedOwnerEnvelopes(ctx context.Context, userID int64, version int, items []models.FileAccessKeyEnvelope) (int, error) {
	stored := 0
	for _, item := range items {
		res, err := p.db.ExecContext(ctx, `
			INSERT INTO file_access_key_envelopes
				(file_id, recipient_cns_user_id, wrapped_dek, dek_wrap_alg, dek_wrap_version,
				 recipient_key_version, access_kind, granted_at, created_at, updated_at)
			SELECT f.id, $2, $3, $4, 1, $5, 'owner', f.created_at, NOW(), NOW()
			FROM files f
			WHERE f.id = $1 AND f.owner_cns_user_id = $2 AND f.is_deleted = FALSE
			ON CONFLICT (file_id, recipient_cns_user_id) DO NOTHING
		`, item.FileID, userID, item.WrappedDEK, item.DEKWrapAlg, version)
		if err != nil {
			return stored, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			stored++
		}
	}
	return stored, nil
}
