package models

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type Tunnel struct {
	ID                 string         `db:"id"                   json:"id"`
	Code               string         `db:"code"                 json:"code"`
	InitiatorCNSUserID int64          `db:"initiator_cns_user_id" json:"initiator_cns_user_id"`
	InitiatorDeviceID  sql.NullString `db:"initiator_device_id"  json:"initiator_device_id"`
	PeerCNSUserID      sql.NullInt64  `db:"peer_cns_user_id"     json:"peer_cns_user_id"`
	PeerDeviceID       sql.NullString `db:"peer_device_id"       json:"peer_device_id"`
	DurationMinutes    int            `db:"duration_minutes"     json:"duration_minutes"`
	Status             string         `db:"status"               json:"status"`
	InitiatorConfirmed bool           `db:"initiator_confirmed"  json:"initiator_confirmed"`
	PeerConfirmed      bool           `db:"peer_confirmed"       json:"peer_confirmed"`
	ExpiresAt          time.Time      `db:"expires_at"           json:"expires_at"`
	CreatedAt          time.Time      `db:"created_at"           json:"created_at"`
	ConfirmedAt        sql.NullTime   `db:"confirmed_at"         json:"confirmed_at"`
	EndedAt            sql.NullTime   `db:"ended_at"             json:"ended_at"`
	EndedByCNSUserID   sql.NullInt64  `db:"ended_by_cns_user_id" json:"ended_by_cns_user_id"`
	EndedByDeviceID    sql.NullString `db:"ended_by_device_id"   json:"ended_by_device_id"`
}

type TunnelFileListItem struct {
	FileID    string    `db:"file_id"   json:"file_id"`
	Filename  string    `db:"filename"  json:"filename"`
	SizeBytes int64     `db:"size_bytes" json:"size_bytes"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
}

type TunnelParticipant struct {
	ID           string          `db:"id"            json:"id"`
	TunnelID     string          `db:"tunnel_id"     json:"tunnel_id"`
	CNSUserID    sql.NullInt64   `db:"cns_user_id"   json:"cns_user_id"`
	DeviceID     sql.NullString  `db:"device_id"     json:"device_id"`
	JoinedAt     time.Time       `db:"joined_at"     json:"joined_at"`
	PublicKeyJWK json.RawMessage `db:"public_key_jwk" json:"public_key_jwk,omitempty"`
	KeyAlgorithm sql.NullString  `db:"key_algorithm" json:"key_algorithm,omitempty"`
	KeyVersion   sql.NullInt32   `db:"key_version"   json:"key_version,omitempty"`
}

// TunnelParticipantPublicKey is what the host sees when polling,
// so it can wrap the session DEK for each guest.
type TunnelParticipantPublicKey struct {
	ParticipantID string          `json:"participant_id"`
	DeviceID      string          `json:"device_id"`
	CNSUserID     int64           `json:"cns_user_id,omitempty"`
	PublicKeyJWK  json.RawMessage `json:"public_key_jwk"`
	KeyAlgorithm  string          `json:"key_algorithm"`
	KeyVersion    int             `json:"key_version"`
	// Set once host has wrapped the DEK for this participant
	HasEnvelope bool `json:"has_envelope"`
}

type TunnelStartRequest struct {
	Duration string `json:"duration" binding:"required"`
	DeviceID string `json:"device_id"`
}

type TunnelStartResponse struct {
	Tunnel       Tunnel              `json:"tunnel"`
	QRPayload    string              `json:"qr_payload"`
	Participants []TunnelParticipant `json:"participants,omitempty"`
}

type TunnelJoinRequest struct {
	Code         string          `json:"code"          binding:"required"`
	DeviceID     string          `json:"device_id"`
	PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
	KeyAlgorithm string          `json:"key_algorithm"`
	KeyVersion   int             `json:"key_version"`
}

type TunnelConfirmRequest struct {
	DeviceID string `json:"device_id"`
}

// TunnelPushEnvelopeRequest — host pushes a wrapped DEK for one participant.
type TunnelPushEnvelopeRequest struct {
	ParticipantDeviceID string `json:"participant_device_id" binding:"required"`
	WrappedDEKB64       string `json:"wrapped_dek_b64"       binding:"required"`
	DEKWrapAlg          string `json:"dek_wrap_alg"          binding:"required"`
	DEKWrapNonceB64     string `json:"dek_wrap_nonce_b64"`
	DEKWrapVersion      int    `json:"dek_wrap_version"`
}

// TunnelGuestEnvelope — what a guest fetches to decrypt the session DEK.
type TunnelGuestEnvelope struct {
	WrappedDEKB64   string `json:"wrapped_dek_b64"`
	DEKWrapAlg      string `json:"dek_wrap_alg"`
	DEKWrapNonceB64 string `json:"dek_wrap_nonce_b64"`
	DEKWrapVersion  int    `json:"dek_wrap_version"`
}

type TunnelPeerWrapKeyResponse struct {
	PeerCNSUserID int64           `json:"peer_cns_user_id"`
	PeerDeviceID  string          `json:"peer_device_id"`
	PublicKeyJWK  json.RawMessage `json:"public_key_jwk"`
	KeyAlgorithm  string          `json:"key_algorithm"`
	KeyVersion    int             `json:"key_version"`
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
	if parsed < 10*time.Minute || parsed > 24*time.Hour {
		return 0, ErrInvalidDuration
	}
	return parsed, nil
}