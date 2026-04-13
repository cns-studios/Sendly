package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type DesktopAPIKey struct {
	ID        string    `db:"id" json:"id"`
	KeyValue  string    `db:"key_value" json:"key_value"`
	OwnerName string    `db:"owner_name" json:"owner_name"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	IsActive  bool      `db:"is_active" json:"is_active"`
}

type DesktopFile struct {
	FileID   string `db:"file_id"`
	APIKeyID string `db:"api_key_id"`
}

type DesktopFileMetadata struct {
	ID          string    `db:"id"           json:"id"`
	NumericCode string    `db:"numeric_code" json:"numeric_code"`
	FileName    string    `db:"file_name"    json:"file_name"`
	FileSize    int64     `db:"file_size"    json:"file_size"`
	ExpiresAt   time.Time `db:"expires_at"   json:"expires_at"`
	UploadedAt  time.Time `db:"uploaded_at"  json:"uploaded_at"`
}


type DesktopVerifyResponse struct {
	Status    string `json:"status"`
	Owner     string `json:"owner"`
}

type DesktopFinalizeRequest struct {
	SessionID       string `json:"session_id" binding:"required"`
	Duration        string `json:"duration" binding:"required"`
	WrappedDEKB64   string `json:"wrapped_dek_b64"`
	DEKWrapAlg      string `json:"dek_wrap_alg"`
	DEKWrapNonceB64 string `json:"dek_wrap_nonce_b64"`
	DEKWrapVersion  int    `json:"dek_wrap_version"`
}
type DesktopFinalizeResponse struct {
	FileID      string    `json:"file_id"`
	NumericCode string    `json:"numeric_code"`
	FileName    string    `json:"file_name"`
	FileSize    int64     `json:"file_size"`
	ExpiresAt   time.Time `json:"expires_at"`
	ShareURL    string    `json:"share_url"`
}

func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var (
	ErrAPIKeyNotFound = &AppError{Code: "API_KEY_NOT_FOUND", Message: "API key not found or inactive"}
	ErrFileNotOwned   = &AppError{Code: "FILE_NOT_OWNED", Message: "file does not belong to this API key"}
)