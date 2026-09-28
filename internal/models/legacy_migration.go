package models

import (
	"encoding/json"
	"time"
)

// Types for migrating accounts from the legacy AES user key to identity keys.
// Remove together with the identity migration (handlers/identity_migration.go).

// LegacyIdentityEscrow is an account's identity private key, encrypted in the
// browser with the account's legacy user key (AES-GCM).
type LegacyIdentityEscrow struct {
	CNSUserID          int64     `db:"cns_user_id"`
	IdentityKeyVersion int       `db:"identity_key_version"`
	WrappedPrivateKey  []byte    `db:"wrapped_private_key"`
	WrapNonce          []byte    `db:"wrap_nonce"`
	WrapAlg            string    `db:"wrap_alg"`
	CreatedAt          time.Time `db:"created_at"`
}

// LegacyFileKey is an owned file whose key is still only wrapped with the
// legacy user key.
type LegacyFileKey struct {
	FileID       string `db:"file_id" json:"file_id"`
	WrappedDEK   []byte `db:"wrapped_dek" json:"-"`
	DEKWrapAlg   string `db:"dek_wrap_alg" json:"dek_wrap_alg"`
	DEKWrapNonce []byte `db:"dek_wrap_nonce" json:"-"`
}

type LegacyUserKeyResponse struct {
	WrappedUKB64 string `json:"wrapped_uk_b64"`
	UKWrapAlg    string `json:"uk_wrap_alg"`
}

// StartIdentityMigrationRequest creates a legacy account's identity key from
// a device holding its user key: the device's own copy plus the escrow.
type StartIdentityMigrationRequest struct {
	DeviceID                     string          `json:"device_id" binding:"required"`
	IdentityPublicKeyJWK         json.RawMessage `json:"identity_public_key_jwk" binding:"required"`
	IdentityKeyAlgorithm         string          `json:"identity_key_algorithm"`
	WrappedIdentityPrivateKeyB64 string          `json:"wrapped_identity_private_key_b64" binding:"required"`
	IdentityKeyWrapAlg           string          `json:"identity_key_wrap_alg" binding:"required"`
	IdentityKeyWrapMeta          json.RawMessage `json:"identity_key_wrap_meta"`
	EscrowWrappedPrivateKeyB64   string          `json:"escrow_wrapped_private_key_b64" binding:"required"`
	EscrowNonceB64               string          `json:"escrow_nonce_b64" binding:"required"`
	EscrowWrapAlg                string          `json:"escrow_wrap_alg" binding:"required"`
}

type LegacyIdentityEscrowResponse struct {
	WrappedPrivateKeyB64 string `json:"wrapped_private_key_b64"`
	WrapNonceB64         string `json:"wrap_nonce_b64"`
	WrapAlg              string `json:"wrap_alg"`
	IdentityKeyVersion   int    `json:"identity_key_version"`
}

// AdoptIdentityKeyRequest stores a legacy device's own copy of the identity
// key, which it decrypted from the escrow with its user key.
type AdoptIdentityKeyRequest struct {
	DeviceID                     string          `json:"device_id" binding:"required"`
	IdentityKeyVersion           int             `json:"identity_key_version" binding:"required"`
	WrappedIdentityPrivateKeyB64 string          `json:"wrapped_identity_private_key_b64" binding:"required"`
	IdentityKeyWrapAlg           string          `json:"identity_key_wrap_alg" binding:"required"`
	IdentityKeyWrapMeta          json.RawMessage `json:"identity_key_wrap_meta"`
}

type LegacyFileKeyItem struct {
	FileID          string `json:"file_id"`
	WrappedDEKB64   string `json:"wrapped_dek_b64"`
	DEKWrapAlg      string `json:"dek_wrap_alg"`
	DEKWrapNonceB64 string `json:"dek_wrap_nonce_b64"`
}

type LegacyFileKeysResponse struct {
	Items []LegacyFileKeyItem `json:"items"`
	// Remaining counts the files still to migrate after this page.
	Remaining int `json:"remaining"`
}

// MigratedFileKeysRequest carries owner envelopes re-wrapped from the legacy
// user key to the identity key.
type MigratedFileKeysRequest struct {
	DeviceID           string             `json:"device_id" binding:"required"`
	IdentityKeyVersion int                `json:"identity_key_version" binding:"required"`
	Items              []RewrappedFileKey `json:"items" binding:"required"`
}
