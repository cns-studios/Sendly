package models

import (
	"database/sql"
	"time"
)

const (
	UserStatusActive   = "active"
	UserStatusInactive = "inactive"
)

// User is this service's local cache of a CNS-provisioned identity: which
// CNS users are known here, plus the profile fields Sendly actually renders
// (username, avatar) so pages don't round-trip to CNS to display them.
type User struct {
	CNSUserID     int64          `db:"cns_user_id" json:"cns_user_id"`
	Username      string         `db:"username" json:"username"`
	AvatarURL     sql.NullString `db:"avatar_url" json:"avatar_url,omitempty"`
	Status        string         `db:"status" json:"status"`
	CreatedAt     time.Time      `db:"created_at" json:"created_at"`
	LastSyncedAt  time.Time      `db:"last_synced_at" json:"last_synced_at"`
	DeactivatedAt sql.NullTime   `db:"deactivated_at" json:"deactivated_at,omitempty"`
}
