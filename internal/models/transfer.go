package models

import (
	"database/sql"
	"time"
)

// Transfer statuses. A transfer starts pending; the recipient accepts it
// (which unlocks their key envelope) or declines it (which deletes it).
const (
	TransferStatusPending  = "pending"
	TransferStatusAccepted = "accepted"
	TransferStatusDeclined = "declined"
)

// Transfer directions, relative to the user listing their transfers.
const (
	TransferDirectionAll      = "all"
	TransferDirectionReceived = "received"
	TransferDirectionSent     = "sent"
)

// Transfer is one user-to-user send of a file (file_transfers). It carries
// no key material: the recipient's wrapped DEK lives in
// file_access_key_envelopes with access_kind = 'share'.
type Transfer struct {
	ID                 string       `db:"id" json:"id"`
	FileID             string       `db:"file_id" json:"file_id"`
	SenderCNSUserID    int64        `db:"sender_cns_user_id" json:"sender_user_id"`
	RecipientCNSUserID int64        `db:"recipient_cns_user_id" json:"recipient_user_id"`
	Status             string       `db:"status" json:"status"`
	CreatedAt          time.Time    `db:"created_at" json:"created_at"`
	RespondedAt        sql.NullTime `db:"responded_at" json:"-"`
}

// TransferListItem is a transfer as the transfers page renders it: file
// metadata, which way it went for the listing user, and both parties.
type TransferListItem struct {
	ID                 string       `db:"id" json:"id"`
	FileID             string       `db:"file_id" json:"file_id"`
	Filename           string       `db:"filename" json:"filename"`
	SizeBytes          int64        `db:"size_bytes" json:"size_bytes"`
	ExpiresAt          time.Time    `db:"expires_at" json:"expires_at"`
	Status             string       `db:"status" json:"status"`
	SentAt             time.Time    `db:"sent_at" json:"sent_at"`
	RespondedAtDB      sql.NullTime `db:"responded_at" json:"-"`
	RespondedAt        *time.Time   `db:"-" json:"responded_at,omitempty"`
	Available          bool         `db:"available" json:"available"`
	Direction          string       `db:"direction" json:"direction"`
	SenderUserID       int64        `db:"sender_user_id" json:"sender_user_id"`
	SenderUsername     string       `db:"sender_username" json:"sender_username"`
	SenderAvatarURL    string       `db:"sender_avatar_url" json:"sender_avatar_url"`
	RecipientUserID    int64        `db:"recipient_user_id" json:"recipient_user_id"`
	RecipientUsername  string       `db:"recipient_username" json:"recipient_username"`
	RecipientAvatarURL string       `db:"recipient_avatar_url" json:"recipient_avatar_url"`
}

type TransfersResponse struct {
	Items      []TransferListItem `json:"items"`
	Page       int                `json:"page"`
	PerPage    int                `json:"per_page"`
	Total      int                `json:"total"`
	TotalPages int                `json:"total_pages"`
}

var (
	ErrTransferExists          = &AppError{Code: "TRANSFER_EXISTS", Message: "This file was already sent to this user"}
	ErrTransferNotFound        = &AppError{Code: "TRANSFER_NOT_FOUND", Message: "Transfer not found"}
	ErrTransferAlreadyAnswered = &AppError{Code: "TRANSFER_ALREADY_ANSWERED", Message: "This transfer was already accepted or declined"}
	ErrTransferFileUnavailable = &AppError{Code: "TRANSFER_FILE_UNAVAILABLE", Message: "This file has expired or was deleted"}
)
