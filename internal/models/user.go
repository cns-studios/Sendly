package models

import (
	"database/sql"
	"time"
)

const (
	UserStatusActive   = "active"
	UserStatusInactive = "inactive"
	// UserStatusDeleted means CNS confirmed the account is gone. It is final:
	// the profile is erased and nothing brings the row back.
	UserStatusDeleted = "deleted"

	// Why a row is inactive. Only stale rows come back on their own when CNS
	// confirms the account still exists; a locked one waits for the user's
	// next request.
	InactiveReasonLocked = "locked"
	InactiveReasonStale  = "stale"
)

// User is this service's local cache of a CNS-provisioned identity: which
// CNS users are known here, plus the profile fields Sendly actually renders
// (username, avatar) so pages don't round-trip to CNS to display them.
type User struct {
	CNSUserID         int64          `db:"cns_user_id" json:"cns_user_id"`
	Username          string         `db:"username" json:"username"`
	AvatarURL         sql.NullString `db:"avatar_url" json:"avatar_url,omitempty"`
	Status            string         `db:"status" json:"status"`
	InactiveReason    sql.NullString `db:"inactive_reason" json:"inactive_reason,omitempty"`
	CreatedAt         time.Time      `db:"created_at" json:"created_at"`
	LastSyncedAt      time.Time      `db:"last_synced_at" json:"last_synced_at"`
	ProfileObservedAt sql.NullTime   `db:"profile_observed_at" json:"profile_observed_at,omitempty"`
	DeactivatedAt     sql.NullTime   `db:"deactivated_at" json:"deactivated_at,omitempty"`
	DeletedAt         sql.NullTime   `db:"deleted_at" json:"deleted_at,omitempty"`
}

// CNSUserProfile is what CNS said about a user at ObservedAt, from any
// source: the user's own service profile, the users:read directory, or a
// user.updated webhook.
type CNSUserProfile struct {
	CNSUserID  int64
	Username   string
	AvatarURL  string
	ObservedAt time.Time
}
