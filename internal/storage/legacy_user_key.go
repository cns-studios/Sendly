package storage

import "context"

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
