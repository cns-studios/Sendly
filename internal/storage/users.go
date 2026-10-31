package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"sendly/internal/models"

	"github.com/jmoiron/sqlx"
)

// The local CNS user cache. Three sources write to it: the user's own
// service profile (UserCache.SyncIfStale), the users:read directory (the
// reconciliation job) and CNS webhooks. All of them go through
// recordProfile and markDeleted, which keep these rules:
//
//   - A profile observed earlier never overwrites one observed later, so a
//     late or reordered webhook can't roll a rename back.
//   - Deletion is final. The username and avatar are erased and the row stays
//     as a tombstone, so nothing recreates the user afterwards.
//   - CNS keeps usernames unique among live accounts. When one row takes a
//     name, any other active row still holding it is out of date (renamed or
//     deleted), so it leaves search and goes first in the re-check queue.

const userColumns = `cns_user_id, username, avatar_url, status, inactive_reason, created_at,
	last_synced_at, profile_observed_at, deactivated_at, deleted_at`

// GetUser returns the local cache row for a CNS user, or ErrUserNotFound if
// this service has never provisioned/synced them.
func (p *Postgres) GetUser(ctx context.Context, cnsUserID int64) (*models.User, error) {
	var user models.User
	err := p.db.GetContext(ctx, &user, `SELECT `+userColumns+` FROM users WHERE cns_user_id = $1`, cnsUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// RecordCNSProfile stores what CNS said about a user. locked is the account
// state when the source reports one (the user's own service profile) and
// nil when it doesn't (directory, webhooks); then a locked row stays locked.
// A deleted row is left alone.
func (p *Postgres) RecordCNSProfile(ctx context.Context, profile models.CNSUserProfile, locked *bool) error {
	return p.inTx(ctx, func(tx *sqlx.Tx) error {
		return recordProfile(ctx, tx, profile, locked, time.Now())
	})
}

// MarkUserDeleted records that CNS deleted the account and erases what this
// service cached about it. It reports whether the row was newly deleted.
func (p *Postgres) MarkUserDeleted(ctx context.Context, cnsUserID int64) (bool, error) {
	var deleted bool
	err := p.inTx(ctx, func(tx *sqlx.Tx) error {
		var err error
		deleted, err = markDeleted(ctx, tx, cnsUserID, time.Now())
		return err
	})
	return deleted, err
}

// ApplyCNSUserUpdated applies a user.updated webhook once per event id. It
// reports false for an event id that was already applied.
func (p *Postgres) ApplyCNSUserUpdated(ctx context.Context, eventID string, profile models.CNSUserProfile) (bool, error) {
	return p.applyWebhookEvent(ctx, eventID, "user.updated", func(tx *sqlx.Tx) error {
		return recordProfile(ctx, tx, profile, nil, time.Now())
	})
}

// ApplyCNSUserDeleted applies a user.deleted webhook once per event id. It
// reports false for an event id that was already applied.
func (p *Postgres) ApplyCNSUserDeleted(ctx context.Context, eventID string, cnsUserID int64) (bool, error) {
	return p.applyWebhookEvent(ctx, eventID, "user.deleted", func(tx *sqlx.Tx) error {
		_, err := markDeleted(ctx, tx, cnsUserID, time.Now())
		return err
	})
}

// applyWebhookEvent records eventID and runs apply in the same transaction,
// so an event is either applied and remembered or neither. A second
// delivery of the same event waits for the first to commit, then skips.
func (p *Postgres) applyWebhookEvent(ctx context.Context, eventID, eventType string, apply func(*sqlx.Tx) error) (bool, error) {
	applied := false
	err := p.inTx(ctx, func(tx *sqlx.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO cns_webhook_events (event_id, event_type) VALUES ($1, $2)
			ON CONFLICT (event_id) DO NOTHING`, eventID, eventType)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if err := apply(tx); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// PurgeCNSWebhookEvents forgets applied webhook event ids received before cutoff.
func (p *Postgres) PurgeCNSWebhookEvents(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM cns_webhook_events WHERE received_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UserSyncCursor is a position in ListUsersToVerify's order.
type UserSyncCursor struct {
	LastSyncedAt time.Time
	CNSUserID    int64
}

// ListUsersToVerify returns users that are not deleted and were last
// confirmed against CNS before syncedBefore, least recently confirmed first,
// starting after cursor. Paging by cursor instead of offset means a row that
// fails to verify doesn't come back again in the same pass.
func (p *Postgres) ListUsersToVerify(ctx context.Context, syncedBefore time.Time, after UserSyncCursor, limit int) ([]models.User, error) {
	users := []models.User{}
	err := p.db.SelectContext(ctx, &users, `
		SELECT `+userColumns+` FROM users
		WHERE status <> $1 AND last_synced_at < $2
		  AND (last_synced_at, cns_user_id) > ($3, $4)
		ORDER BY last_synced_at, cns_user_id
		LIMIT $5`, models.UserStatusDeleted, syncedBefore, after.LastSyncedAt, after.CNSUserID, limit)
	return users, err
}

// SearchUsersByUsername finds active local users whose username contains
// query (case-insensitive), for the share-recipient picker.
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

// likeEscaper escapes LIKE metacharacters so user input matches literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// DeactivateStaleUsers marks active users inactive ('stale') if CNS hasn't
// confirmed them since staleBefore. With the directory working every row is
// re-confirmed daily, so this only bites when CNS can't be reached; the row
// comes back as soon as CNS confirms the account again.
func (p *Postgres) DeactivateStaleUsers(ctx context.Context, staleBefore time.Time) (int64, error) {
	result, err := p.db.ExecContext(ctx, `
		UPDATE users
		SET status = $1, inactive_reason = $2, deactivated_at = NOW()
		WHERE status = $3 AND last_synced_at < $4
	`, models.UserStatusInactive, models.InactiveReasonStale, models.UserStatusActive, staleBefore)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (p *Postgres) inTx(ctx context.Context, fn func(*sqlx.Tx) error) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// lockUser returns the user's row locked for update, or nil if there is none.
func lockUser(ctx context.Context, tx *sqlx.Tx, cnsUserID int64) (*models.User, error) {
	var user models.User
	err := tx.GetContext(ctx, &user, `SELECT `+userColumns+` FROM users WHERE cns_user_id = $1 FOR UPDATE`, cnsUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// lockOrCreateUser locks the user's row, first inserting next as the row if
// there is none. created reports whether next was inserted.
func lockOrCreateUser(ctx context.Context, tx *sqlx.Tx, cnsUserID int64, next func(*models.User) models.User) (cur *models.User, created bool, err error) {
	cur, err = lockUser(ctx, tx, cnsUserID)
	if err != nil || cur != nil {
		return cur, false, err
	}
	row := next(nil)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO users (cns_user_id, username, avatar_url, status, inactive_reason, created_at,
			last_synced_at, profile_observed_at, deactivated_at, deleted_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6, $7, $8, $9)
		ON CONFLICT (cns_user_id) DO NOTHING`,
		row.CNSUserID, row.Username, row.AvatarURL, row.Status, row.InactiveReason,
		row.LastSyncedAt, row.ProfileObservedAt, row.DeactivatedAt, row.DeletedAt)
	if err != nil {
		return nil, false, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, false, err
	} else if n == 1 {
		return &row, true, nil
	}
	// Another writer inserted the row first; work on theirs.
	cur, err = lockUser(ctx, tx, cnsUserID)
	if err == nil && cur == nil {
		err = errors.New("user row vanished during upsert")
	}
	return cur, false, err
}

func writeUser(ctx context.Context, tx *sqlx.Tx, u models.User) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE users SET username = $2, avatar_url = $3, status = $4, inactive_reason = $5,
			last_synced_at = $6, profile_observed_at = $7, deactivated_at = $8, deleted_at = $9
		WHERE cns_user_id = $1`,
		u.CNSUserID, u.Username, u.AvatarURL, u.Status, u.InactiveReason,
		u.LastSyncedAt, u.ProfileObservedAt, u.DeactivatedAt, u.DeletedAt)
	return err
}

func recordProfile(ctx context.Context, tx *sqlx.Tx, profile models.CNSUserProfile, locked *bool, now time.Time) error {
	if profile.CNSUserID <= 0 || strings.TrimSpace(profile.Username) == "" {
		return errors.New("cns profile needs a user id and a username")
	}
	cur, created, err := lockOrCreateUser(ctx, tx, profile.CNSUserID, func(cur *models.User) models.User {
		return NextUserState(cur, profile, locked, now)
	})
	if err != nil {
		return err
	}
	if !created {
		if cur.Status == models.UserStatusDeleted {
			return nil
		}
		next := NextUserState(cur, profile, locked, now)
		if err := writeUser(ctx, tx, next); err != nil {
			return err
		}
		cur = &next
	}
	return demoteNameHolders(ctx, tx, *cur, now)
}

// demoteNameHolders takes u's username away from every other active row
// whose profile is older than u's: CNS can't have given the name to both.
func demoteNameHolders(ctx context.Context, tx *sqlx.Tx, u models.User, now time.Time) error {
	if u.Status != models.UserStatusActive || !u.ProfileObservedAt.Valid {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE users
		SET status = $3, inactive_reason = $4, deactivated_at = COALESCE(deactivated_at, $5),
			last_synced_at = 'epoch'
		WHERE LOWER(username) = LOWER($2) AND cns_user_id <> $1 AND status = $6
		  AND (profile_observed_at IS NULL OR profile_observed_at < $7)`,
		u.CNSUserID, u.Username, models.UserStatusInactive, models.InactiveReasonStale, now,
		models.UserStatusActive, u.ProfileObservedAt.Time)
	return err
}

func markDeleted(ctx context.Context, tx *sqlx.Tx, cnsUserID int64, now time.Time) (bool, error) {
	if cnsUserID <= 0 {
		return false, errors.New("cns user id must be positive")
	}
	cur, created, err := lockOrCreateUser(ctx, tx, cnsUserID, func(cur *models.User) models.User {
		return DeletedUserState(cur, cnsUserID, now)
	})
	if err != nil {
		return false, err
	}
	if !created {
		if cur.Status == models.UserStatusDeleted {
			return false, nil
		}
		if err := writeUser(ctx, tx, DeletedUserState(cur, cnsUserID, now)); err != nil {
			return false, err
		}
	}
	// Files keep a copy of the owner's username for display.
	if _, err := tx.ExecContext(ctx, `
		UPDATE files SET owner_cns_username = NULL
		WHERE owner_cns_user_id = $1 AND owner_cns_username IS NOT NULL`, cnsUserID); err != nil {
		return false, err
	}
	return true, nil
}

// NextUserState is cur after taking in profile at now. cur is nil for a user
// this service hasn't seen. The caller must not apply it to a deleted row.
func NextUserState(cur *models.User, profile models.CNSUserProfile, locked *bool, now time.Time) models.User {
	// CNS confirmed the account existed at the time of the observation; a
	// clock ahead of ours doesn't get to confirm the future.
	confirmedAt := profile.ObservedAt
	if confirmedAt.IsZero() || confirmedAt.After(now) {
		confirmedAt = now
	}

	var next models.User
	if cur == nil {
		next = models.User{CNSUserID: profile.CNSUserID, Status: models.UserStatusActive}
	} else {
		next = *cur
	}

	if !next.ProfileObservedAt.Valid || !confirmedAt.Before(next.ProfileObservedAt.Time) {
		next.Username = profile.Username
		next.AvatarURL = sql.NullString{String: profile.AvatarURL, Valid: profile.AvatarURL != ""}
		next.ProfileObservedAt = sql.NullTime{Time: confirmedAt, Valid: true}
	}

	confirmsNewer := cur == nil || confirmedAt.After(cur.LastSyncedAt)
	if confirmsNewer {
		next.LastSyncedAt = confirmedAt
	}

	switch {
	case locked != nil && *locked:
		next.Status = models.UserStatusInactive
		next.InactiveReason = sql.NullString{String: models.InactiveReasonLocked, Valid: true}
	case locked != nil:
		next.Status = models.UserStatusActive
	case next.Status == models.UserStatusInactive && next.InactiveReason.String == models.InactiveReasonStale && confirmsNewer:
		next.Status = models.UserStatusActive
	}

	if next.Status == models.UserStatusActive {
		next.InactiveReason = sql.NullString{}
		next.DeactivatedAt = sql.NullTime{}
	} else if !next.DeactivatedAt.Valid {
		next.DeactivatedAt = sql.NullTime{Time: now, Valid: true}
	}
	return next
}

// DeletedUserState is cur (nil if unseen) after CNS deleted the account:
// a tombstone with the profile erased.
func DeletedUserState(cur *models.User, cnsUserID int64, now time.Time) models.User {
	next := models.User{CNSUserID: cnsUserID, LastSyncedAt: now, DeactivatedAt: sql.NullTime{Time: now, Valid: true}}
	if cur != nil {
		next.CreatedAt = cur.CreatedAt
		next.LastSyncedAt = cur.LastSyncedAt
		if cur.DeactivatedAt.Valid {
			next.DeactivatedAt = cur.DeactivatedAt
		}
	}
	next.Status = models.UserStatusDeleted
	next.DeletedAt = sql.NullTime{Time: now, Valid: true}
	return next
}
