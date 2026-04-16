package models

import (
	"database/sql"
	"strings"
	"time"
)

type Tunnel struct {
	ID                 string         `db:"id" json:"id"`
	Code               string         `db:"code" json:"code"`
	InitiatorCNSUserID int64          `db:"initiator_cns_user_id" json:"initiator_cns_user_id"`
	InitiatorDeviceID   sql.NullString `db:"initiator_device_id" json:"initiator_device_id"`
	PeerCNSUserID      sql.NullInt64  `db:"peer_cns_user_id" json:"peer_cns_user_id"`
	PeerDeviceID       sql.NullString `db:"peer_device_id" json:"peer_device_id"`
	DurationMinutes    int            `db:"duration_minutes" json:"duration_minutes"`
	Status             string         `db:"status" json:"status"`
	InitiatorConfirmed bool           `db:"initiator_confirmed" json:"initiator_confirmed"`
	PeerConfirmed      bool           `db:"peer_confirmed" json:"peer_confirmed"`
	ExpiresAt          time.Time      `db:"expires_at" json:"expires_at"`
	CreatedAt          time.Time      `db:"created_at" json:"created_at"`
	ConfirmedAt        sql.NullTime   `db:"confirmed_at" json:"confirmed_at"`
	EndedAt            sql.NullTime   `db:"ended_at" json:"ended_at"`
	EndedByCNSUserID   sql.NullInt64  `db:"ended_by_cns_user_id" json:"ended_by_cns_user_id"`
	EndedByDeviceID    sql.NullString `db:"ended_by_device_id" json:"ended_by_device_id"`
}

type TunnelFileListItem struct {
	FileID    string    `db:"file_id" json:"file_id"`
	Filename  string    `db:"filename" json:"filename"`
	SizeBytes int64     `db:"size_bytes" json:"size_bytes"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
}

type TunnelStartRequest struct {
	Duration string `json:"duration" binding:"required"`
	DeviceID string `json:"device_id"`
}

type TunnelStartResponse struct {
	Tunnel    Tunnel `json:"tunnel"`
	QRPayload string `json:"qr_payload"`
}

type TunnelJoinRequest struct {
	Code     string `json:"code" binding:"required"`
	DeviceID string `json:"device_id"`
}

type TunnelConfirmRequest struct {
	DeviceID string `json:"device_id"`
}

type TunnelEndRequest struct {
	DeviceID string `json:"device_id"`
}

const (
	TunnelStatusPending = "pending"
	TunnelStatusJoined  = "joined"
	TunnelStatusActive  = "active"
	TunnelStatusEnded   = "ended"
	TunnelStatusExpired = "expired"
)

func ParseTunnelDuration(d string) (time.Duration, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0, ErrInvalidDuration
	}

	parsed, err := time.ParseDuration(d)
	if err != nil {
		return 0, ErrInvalidDuration
	}
	if parsed < 10*time.Minute || parsed > 12*time.Hour {
		return 0, ErrInvalidDuration
	}
	return parsed, nil
}