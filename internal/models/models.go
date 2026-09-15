package models

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"time"
)

type File struct {
	ID               string         `db:"id" json:"id"`
	NumericCode      string         `db:"numeric_code" json:"numeric_code"`
	OriginalName     string         `db:"original_name" json:"original_name"`
	SizeBytes        int64          `db:"size_bytes" json:"size_bytes"`
	UploaderIP       string         `db:"uploader_ip" json:"-"`
	OwnerCNSUserID   sql.NullInt64  `db:"owner_cns_user_id" json:"-"`
	OwnerCNSUserName sql.NullString `db:"owner_cns_username" json:"-"`
	TunnelID         sql.NullString `db:"tunnel_id" json:"-"`
	ExpiresAt        time.Time      `db:"expires_at" json:"expires_at"`
	CreatedAt        time.Time      `db:"created_at" json:"created_at"`
	ReportCount      int            `db:"report_count" json:"-"`
	IsDeleted        bool           `db:"is_deleted" json:"-"`
}

type OwnedFileListItem struct {
	FileID    string    `db:"file_id" json:"file_id"`
	Filename  string    `db:"filename" json:"filename"`
	SizeBytes int64     `db:"size_bytes" json:"size_bytes"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	ShareURL  string    `json:"share_url"`
}

type FileKeyEnvelope struct {
	FileID         string    `db:"file_id" json:"file_id"`
	WrappedDEK     []byte    `db:"wrapped_dek" json:"-"`
	DEKWrapAlg     string    `db:"dek_wrap_alg" json:"dek_wrap_alg"`
	DEKWrapNonce   []byte    `db:"dek_wrap_nonce" json:"-"`
	DEKWrapVersion int       `db:"dek_wrap_version" json:"dek_wrap_version"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}

type FileRecipientKeyEnvelope struct {
	FileID             string    `db:"file_id" json:"file_id"`
	RecipientCNSUserID int64     `db:"recipient_cns_user_id" json:"recipient_cns_user_id"`
	RecipientDeviceID  string    `db:"recipient_device_id" json:"recipient_device_id"`
	WrappedDEK         []byte    `db:"wrapped_dek" json:"-"`
	DEKWrapAlg         string    `db:"dek_wrap_alg" json:"dek_wrap_alg"`
	DEKWrapNonce       []byte    `db:"dek_wrap_nonce" json:"-"`
	DEKWrapVersion     int       `db:"dek_wrap_version" json:"dek_wrap_version"`
	CreatedAt          time.Time `db:"created_at" json:"created_at"`
}

type UserDevice struct {
	ID           string          `db:"id" json:"id"`
	CNSUserID    int64           `db:"cns_user_id" json:"cns_user_id"`
	DeviceLabel  string          `db:"device_label" json:"device_label"`
	PublicKeyJWK json.RawMessage `db:"public_key_jwk" json:"public_key_jwk"`
	KeyAlgorithm string          `db:"key_algorithm" json:"key_algorithm"`
	KeyVersion   int             `db:"key_version" json:"key_version"`
	CreatedAt    time.Time       `db:"created_at" json:"created_at"`
	LastSeenAt   time.Time       `db:"last_seen_at" json:"last_seen_at"`
	RevokedAt    sql.NullTime    `db:"revoked_at" json:"-"`
}

type UserKeyEnvelope struct {
	ID             string          `db:"id" json:"id"`
	CNSUserID      int64           `db:"cns_user_id" json:"cns_user_id"`
	DeviceID       string          `db:"device_id" json:"device_id"`
	WrappedUserKey []byte          `db:"wrapped_user_key" json:"-"`
	UKWrapAlg      string          `db:"uk_wrap_alg" json:"uk_wrap_alg"`
	UKWrapMeta     json.RawMessage `db:"uk_wrap_meta" json:"uk_wrap_meta"`
	KeyVersion     int             `db:"key_version" json:"key_version"`
	CreatedAt      time.Time       `db:"created_at" json:"created_at"`
}

type UserIdentityKey struct {
	CNSUserID    int64           `db:"cns_user_id" json:"cns_user_id"`
	KeyVersion   int             `db:"key_version" json:"key_version"`
	PublicKeyJWK json.RawMessage `db:"public_key_jwk" json:"public_key_jwk"`
	KeyAlgorithm string          `db:"key_algorithm" json:"key_algorithm"`
	Status       string          `db:"status" json:"status"`
	CreatedAt    time.Time       `db:"created_at" json:"created_at"`
	ActivatedAt  sql.NullTime    `db:"activated_at" json:"-"`
	RetiredAt    sql.NullTime    `db:"retired_at" json:"-"`
}

type UserIdentityKeyDeviceEnvelope struct {
	ID                 string          `db:"id" json:"id"`
	CNSUserID          int64           `db:"cns_user_id" json:"cns_user_id"`
	DeviceID           string          `db:"device_id" json:"device_id"`
	IdentityKeyVersion int             `db:"identity_key_version" json:"identity_key_version"`
	WrappedPrivateKey  []byte          `db:"wrapped_private_key" json:"-"`
	WrapAlg            string          `db:"wrap_alg" json:"wrap_alg"`
	WrapMeta           json.RawMessage `db:"wrap_meta" json:"wrap_meta"`
	CreatedAt          time.Time       `db:"created_at" json:"created_at"`
}

type FileAccessKeyEnvelope struct {
	FileID              string         `db:"file_id" json:"file_id"`
	RecipientCNSUserID  int64          `db:"recipient_cns_user_id" json:"recipient_cns_user_id"`
	WrappedDEK          []byte         `db:"wrapped_dek" json:"-"`
	DEKWrapAlg          string         `db:"dek_wrap_alg" json:"dek_wrap_alg"`
	DEKWrapNonce        []byte         `db:"dek_wrap_nonce" json:"-"`
	DEKWrapVersion      int            `db:"dek_wrap_version" json:"dek_wrap_version"`
	RecipientKeyVersion int            `db:"recipient_key_version" json:"recipient_key_version"`
	AccessKind          string         `db:"access_kind" json:"access_kind"`
	SourceTunnelID      sql.NullString `db:"source_tunnel_id" json:"source_tunnel_id"`
	GrantedAt           time.Time      `db:"granted_at" json:"granted_at"`
	CreatedAt           time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at" json:"updated_at"`
}

type DeviceEnrollment struct {
	ID                 string         `db:"id" json:"id"`
	CNSUserID          int64          `db:"cns_user_id" json:"cns_user_id"`
	RequestDeviceID    string         `db:"request_device_id" json:"request_device_id"`
	VerificationCode   string         `db:"verification_code" json:"verification_code"`
	Status             string         `db:"status" json:"status"`
	ApprovedByDeviceID sql.NullString `db:"approved_by_device_id" json:"-"`
	ExpiresAt          time.Time      `db:"expires_at" json:"expires_at"`
	CreatedAt          time.Time      `db:"created_at" json:"created_at"`
	ApprovedAt         sql.NullTime   `db:"approved_at" json:"-"`
}

type PendingEnrollmentItem struct {
	Enrollment    DeviceEnrollment `json:"enrollment"`
	RequestDevice UserDevice       `json:"request_device"`
}

type PendingEnrollmentsResponse struct {
	Items []PendingEnrollmentItem `json:"items"`
}

type FileMetadata struct {
	ID           string    `json:"id"`
	NumericCode  string    `json:"numeric_code"`
	OriginalName string    `json:"original_name"`
	SizeBytes    int64     `json:"size_bytes"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
}

func (f *File) ToMetadata() *FileMetadata {
	return &FileMetadata{
		ID:           f.ID,
		NumericCode:  f.NumericCode,
		OriginalName: f.OriginalName,
		SizeBytes:    f.SizeBytes,
		ExpiresAt:    f.ExpiresAt,
		CreatedAt:    f.CreatedAt,
	}
}

type Report struct {
	ID         int       `db:"id" json:"id"`
	FileID     string    `db:"file_id" json:"file_id"`
	ReporterIP string    `db:"reporter_ip" json:"reporter_ip"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

type UploadSession struct {
	SessionID    string    `json:"session_id"`
	FileID       string    `json:"file_id"`
	OriginalName string    `json:"original_name"`
	TotalSize    int64     `json:"total_size"`
	TotalChunks  int       `json:"total_chunks"`
	ChunkSize    int64     `json:"chunk_size"`
	UploaderIP   string    `json:"uploader_ip"`
	CreatedAt    time.Time `json:"created_at"`
}

type UploadInitRequest struct {
	FileName    string `json:"file_name" binding:"required"`
	FileSize    int64  `json:"file_size" binding:"required,gt=0"`
	TotalChunks int    `json:"total_chunks" binding:"required,gt=0"`
	ChunkSize   int64  `json:"chunk_size" binding:"required,gt=0"`
	TunnelID    string `json:"tunnel_id"`
}

type UploadInitResponse struct {
	SessionID   string `json:"session_id"`
	FileID      string `json:"file_id"`
	ChunkSize   int64  `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
}

type UploadChunkRequest struct {
	SessionID  string `form:"session_id" binding:"required"`
	ChunkIndex int    `form:"chunk_index" binding:"gte=0"`
}

type UploadCompleteRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Confirmed bool   `json:"confirmed" binding:"required"`
}

type UploadCompleteResponse struct {
	SessionID        string    `json:"session_id"`
	FileID           string    `json:"file_id"`
	PendingExpiresAt time.Time `json:"pending_expires_at"`
}

type UploadFinalizeRequest struct {
	SessionID               string `json:"session_id" binding:"required"`
	Duration                string `json:"duration"`
	TunnelID                string `json:"tunnel_id"`
	DeviceID                string `json:"device_id"`
	WrappedDEKB64           string `json:"wrapped_dek_b64"`
	PeerWrappedDEKB64       string `json:"peer_wrapped_dek_b64"`
	DEKWrapAlg              string `json:"dek_wrap_alg"`
	DEKWrapNonceB64         string `json:"dek_wrap_nonce_b64"`
	DEKWrapVersion          int    `json:"dek_wrap_version"`
	PeerDEKWrapAlg          string `json:"peer_dek_wrap_alg"`
	PeerDEKWrapNonceB64     string `json:"peer_dek_wrap_nonce_b64"`
	PeerDEKWrapVersion      int    `json:"peer_dek_wrap_version"`
	IdentityWrappedDEKB64   string `json:"identity_wrapped_dek_b64"`
	IdentityDEKWrapAlg      string `json:"identity_dek_wrap_alg"`
	IdentityDEKWrapNonceB64 string `json:"identity_dek_wrap_nonce_b64"`
	IdentityDEKWrapVersion  int    `json:"identity_dek_wrap_version"`
	IdentityKeyVersion      int    `json:"identity_key_version"`
}

type UploadFinalizeResponse struct {
	FileID      string `json:"file_id"`
	NumericCode string `json:"numeric_code"`
	ShareURL    string `json:"share_url"`
}

type RecentUploadsResponse struct {
	Items      []OwnedFileListItem `json:"items"`
	Page       int                 `json:"page"`
	PerPage    int                 `json:"per_page"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"total_pages"`
	Query      string              `json:"query,omitempty"`
}

type FileAccessResponse struct {
	File                       FileMetadata             `json:"file"`
	FileKeyEnvelope            FileKeyEnvelopeResponse  `json:"file_key_envelope"`
	UserKeyEnvelope            UserKeyEnvelopeResponse  `json:"user_key_envelope"`
	IdentityFileAccessEnvelope *FileKeyEnvelopeResponse `json:"file_access_key_envelope,omitempty"`
}

type ShareFileRequest struct {
	RecipientUserID     int64  `json:"recipient_user_id" binding:"required"`
	WrappedDEK          string `json:"wrapped_dek" binding:"required"`
	DEKWrapAlg          string `json:"dek_wrap_alg" binding:"required"`
	DEKWrapNonce        string `json:"dek_wrap_nonce"`
	RecipientKeyVersion int    `json:"recipient_key_version" binding:"required"`
}

type SharedFilesResponse struct {
	Items      []OwnedFileListItem `json:"items"`
	Page       int                 `json:"page"`
	PerPage    int                 `json:"per_page"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"total_pages"`
}

type FileKeyEnvelopeResponse struct {
	WrappedDEKB64   string `json:"wrapped_dek_b64"`
	DEKWrapAlg      string `json:"dek_wrap_alg"`
	DEKWrapNonceB64 string `json:"dek_wrap_nonce_b64,omitempty"`
	DEKWrapVersion  int    `json:"dek_wrap_version"`
}

type UserKeyEnvelopeResponse struct {
	WrappedUKB64 string          `json:"wrapped_uk_b64"`
	UKWrapAlg    string          `json:"uk_wrap_alg"`
	UKWrapMeta   json.RawMessage `json:"uk_wrap_meta"`
	KeyVersion   int             `json:"key_version"`
}

type UserIdentityKeyDeviceEnvelopeResponse struct {
	WrappedPrivateKeyB64 string          `json:"wrapped_private_key_b64"`
	WrapAlg              string          `json:"wrap_alg"`
	WrapMeta             json.RawMessage `json:"wrap_meta"`
	IdentityKeyVersion   int             `json:"identity_key_version"`
}

type DeviceRegisterRequest struct {
	DeviceID          string          `json:"device_id" binding:"required"`
	DeviceLabel       string          `json:"device_label"`
	PublicKeyJWK      json.RawMessage `json:"public_key_jwk" binding:"required"`
	KeyAlgorithm      string          `json:"key_algorithm" binding:"required"`
	KeyVersion        int             `json:"key_version"`
	WrappedUserKeyB64 string          `json:"wrapped_user_key_b64"`
	UKWrapAlg         string          `json:"uk_wrap_alg"`
	UKWrapMeta        json.RawMessage `json:"uk_wrap_meta"`

	// Additive identity keypair fields
	IdentityPublicKeyJWK         json.RawMessage `json:"identity_public_key_jwk,omitempty"`
	IdentityKeyAlgorithm         string          `json:"identity_key_algorithm,omitempty"`
	IdentityKeyVersion           int             `json:"identity_key_version,omitempty"`
	WrappedIdentityPrivateKeyB64 string          `json:"wrapped_identity_private_key_b64,omitempty"`
	IdentityKeyWrapAlg           string          `json:"identity_key_wrap_alg,omitempty"`
	IdentityKeyWrapMeta          json.RawMessage `json:"identity_key_wrap_meta,omitempty"`
}

type DeviceRegisterResponse struct {
	DeviceID            string                                 `json:"device_id"`
	NeedsEnrollment     bool                                   `json:"needs_enrollment"`
	UserKeyEnvelope     *UserKeyEnvelopeResponse               `json:"user_key_envelope,omitempty"`
	IdentityKeyEnvelope *UserIdentityKeyDeviceEnvelopeResponse `json:"identity_key_envelope,omitempty"`
}

type DeviceRenameRequest struct {
	DeviceLabel string `json:"device_label" binding:"required"`
}

type CreateEnrollmentRequest struct {
	RequestDeviceID string `json:"request_device_id" binding:"required"`
}

type CreateEnrollmentResponse struct {
	EnrollmentID     string    `json:"enrollment_id"`
	VerificationCode string    `json:"verification_code"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type ApproveEnrollmentRequest struct {
	ApproverDeviceID  string          `json:"approver_device_id" binding:"required"`
	VerificationCode  string          `json:"verification_code" binding:"required"`
	WrappedUserKeyB64 string          `json:"wrapped_user_key_b64" binding:"required"`
	UKWrapAlg         string          `json:"uk_wrap_alg" binding:"required"`
	UKWrapMeta        json.RawMessage `json:"uk_wrap_meta" binding:"required"`

	// Additive identity keypair envelope fields
	WrappedIdentityPrivateKeyB64 string          `json:"wrapped_identity_private_key_b64,omitempty"`
	IdentityKeyWrapAlg           string          `json:"identity_key_wrap_alg,omitempty"`
	IdentityKeyWrapMeta          json.RawMessage `json:"identity_key_wrap_meta,omitempty"`
	IdentityKeyVersion           int             `json:"identity_key_version,omitempty"`
}

type RejectEnrollmentRequest struct {
	ApproverDeviceID string `json:"approver_device_id" binding:"required"`
}

const (
	EnrollmentStatusPending  = "pending"
	EnrollmentStatusApproved = "approved"
	EnrollmentStatusRejected = "rejected"
	EnrollmentStatusExpired  = "expired"
)

type UploadCancelRequest struct {
	SessionID string `json:"session_id" binding:"required"`
}

type ReportRequest struct {
	FileID string `json:"file_id" binding:"required"`
}

type ReportResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type DiscordWebhookPayload struct {
	Content string  `json:"content,omitempty"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

type Embed struct {
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"`
	Fields      []Field `json:"fields,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
}

type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

const (
	Duration24Hours = "24h"
	Duration7Days   = "7d"
	Duration30Days  = "30d"
	Duration90Days  = "90d"
)

func ParseDuration(d string) (time.Duration, error) {
	switch d {
	case Duration24Hours:
		return 24 * time.Hour, nil
	case Duration7Days:
		return 7 * 24 * time.Hour, nil
	case Duration30Days:
		return 30 * 24 * time.Hour, nil
	case Duration90Days:
		return 90 * 24 * time.Hour, nil
	default:
		return 0, ErrInvalidDuration
	}
}

func ParseFinalizeDuration(d string) (time.Duration, error) {
	switch d {
	case Duration24Hours:
		return 24 * time.Hour, nil
	case Duration7Days:
		return 7 * 24 * time.Hour, nil
	case Duration30Days:
		return 30 * 24 * time.Hour, nil
	case Duration90Days:
		return 90 * 24 * time.Hour, nil
	default:
		return 0, ErrInvalidDuration
	}
}

type AppError struct {
	Code    string
	Message string
}

func (e *AppError) Error() string {
	return e.Message
}

var (
	ErrInvalidDuration          = &AppError{Code: "INVALID_DURATION", Message: "invalid duration specified"}
	ErrFileTooLarge             = &AppError{Code: "FILE_TOO_LARGE", Message: "file exceeds maximum size limit"}
	ErrFileNotFound             = &AppError{Code: "FILE_NOT_FOUND", Message: "file not found"}
	ErrIdentityKeyNotFound      = &AppError{Code: "IDENTITY_KEY_NOT_FOUND", Message: "identity key not found"}
	ErrDeviceEnvelopeNotFound   = &AppError{Code: "DEVICE_ENVELOPE_NOT_FOUND", Message: "identity key device envelope not found"}
	ErrFileAccessNotFound       = &AppError{Code: "FILE_ACCESS_NOT_FOUND", Message: "file access grant not found"}
	ErrRecipientNotReady        = &AppError{Code: "RECIPIENT_NOT_READY", Message: "recipient has not set up sharing yet"}
	ErrFileExpired              = &AppError{Code: "FILE_EXPIRED", Message: "file has expired"}
	ErrFileDeleted              = &AppError{Code: "FILE_DELETED", Message: "file has been deleted"}
	ErrDeviceNotFound           = &AppError{Code: "DEVICE_NOT_FOUND", Message: "device not found"}
	ErrWrappedUserKeyRequired   = &AppError{Code: "WRAPPED_UK_REQUIRED", Message: "wrapped user key is required"}
	ErrDeviceNotAuthorized      = &AppError{Code: "DEVICE_NOT_AUTHORIZED", Message: "device does not belong to user"}
	ErrApproverNotTrusted       = &AppError{Code: "APPROVER_NOT_TRUSTED", Message: "approver device is not trusted"}
	ErrEnrollmentNotPending     = &AppError{Code: "ENROLLMENT_NOT_PENDING", Message: "enrollment is no longer pending"}
	ErrVerificationCodeMismatch = &AppError{Code: "VERIFICATION_CODE_MISMATCH", Message: "verification code mismatch"}
	ErrSessionNotFound          = &AppError{Code: "SESSION_NOT_FOUND", Message: "upload session not found"}
	ErrSessionExpired           = &AppError{Code: "SESSION_EXPIRED", Message: "upload session has expired"}
	ErrUploadNotPending         = &AppError{Code: "UPLOAD_NOT_PENDING", Message: "upload is no longer pending"}
	ErrInvalidChunk             = &AppError{Code: "INVALID_CHUNK", Message: "invalid chunk index"}
	ErrChunkAlreadyExists       = &AppError{Code: "CHUNK_EXISTS", Message: "chunk already uploaded"}
	ErrUploadIncomplete         = &AppError{Code: "UPLOAD_INCOMPLETE", Message: "not all chunks have been uploaded"}
	ErrRateLimited              = &AppError{Code: "RATE_LIMITED", Message: "too many requests, please slow down"}
	ErrInvalidCode              = &AppError{Code: "INVALID_CODE", Message: "invalid numeric code"}
)

func GenerateID(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func GenerateNumericCode() string {
	const charset = "0123456789"
	result := make([]byte, 12)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func GenerateSessionID() string {
	return GenerateID(32)
}
