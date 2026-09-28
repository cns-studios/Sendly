package storage

import (
	"context"

	"sendly/internal/models"
)

// After a recovery the account's file keys are still wrapped for the older
// identity key version; a returning device that holds that version re-wraps
// them for the active one.

// lockedFileKeys selects the user's live file keys wrapped for an identity key
// version older than the active one.
const lockedFileKeys = `
	FROM file_access_key_envelopes e
	JOIN files f ON f.id = e.file_id
	WHERE e.recipient_cns_user_id = $1
	  AND e.recipient_key_version = $2
	  AND e.recipient_key_version < (
		SELECT key_version FROM user_identity_keys
		WHERE cns_user_id = $1 AND status = 'active'
	  )
	  AND f.is_deleted = FALSE
	  AND f.expires_at > NOW()
`

// ListLockedFileKeys returns up to limit of the user's file keys wrapped for
// identity key version (older than the active one) with a file id after the
// given one, and how many remain after them.
func (p *Postgres) ListLockedFileKeys(ctx context.Context, userID int64, version int, after string, limit int) ([]models.FileAccessKeyEnvelope, int, error) {
	items := []models.FileAccessKeyEnvelope{}
	if err := p.db.SelectContext(ctx, &items, `
		SELECT e.file_id, e.recipient_cns_user_id, e.wrapped_dek, e.dek_wrap_alg, e.dek_wrap_nonce,
			e.dek_wrap_version, e.recipient_key_version, e.access_kind, e.source_tunnel_id,
			e.granted_at, e.created_at, e.updated_at
	`+lockedFileKeys+`
		  AND e.file_id > $3
		ORDER BY e.file_id
		LIMIT $4
	`, userID, version, after, limit); err != nil {
		return nil, 0, err
	}
	last := after
	if len(items) > 0 {
		last = items[len(items)-1].FileID
	}
	var remaining int
	err := p.db.GetContext(ctx, &remaining, `SELECT COUNT(*) `+lockedFileKeys+` AND e.file_id > $3`, userID, version, last)
	return items, remaining, err
}

// RescueLockedFileKeys replaces the user's file keys that are wrapped for
// fromVersion with ones for the active version toVersion. Only keys that are
// still locked (fromVersion older than the active version) are replaced, so a
// rescue can never overwrite a key the user can open. It returns how many
// were replaced.
func (p *Postgres) RescueLockedFileKeys(ctx context.Context, userID int64, fromVersion, toVersion int, items []models.FileAccessKeyEnvelope) (int, error) {
	rescued := 0
	for _, item := range items {
		res, err := p.db.ExecContext(ctx, `
			UPDATE file_access_key_envelopes
			SET wrapped_dek = $4, dek_wrap_alg = $5, dek_wrap_nonce = NULL,
				recipient_key_version = $3, updated_at = NOW()
			WHERE file_id = $1 AND recipient_cns_user_id = $2
			  AND recipient_key_version = $6
			  AND $6 < $3
			  AND $3 = (
				SELECT key_version FROM user_identity_keys
				WHERE cns_user_id = $2 AND status = 'active'
			  )
		`, item.FileID, userID, toVersion, item.WrappedDEK, item.DEKWrapAlg, fromVersion)
		if err != nil {
			return rescued, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rescued++
		}
	}
	return rescued, nil
}
