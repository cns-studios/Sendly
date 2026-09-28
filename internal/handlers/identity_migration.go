package handlers

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

// IdentityMigrationHandler moves accounts from before identity keys onto
// them. Temporary: delete this file, storage/legacy_user_key.go,
// models/legacy_migration.go, static/js/identity-migration.js and its routes
// and script tags once every account is migrated, then drop
// user_key_envelopes and legacy_identity_escrow.
//
// Everything is decrypted and re-encrypted in the browser. A device holding
// the account's legacy user key creates the identity key and stores it
// encrypted with the user key (the escrow), so the account's other legacy
// devices can pick it up without approval, and re-wraps the account's files
// for the identity key.
type IdentityMigrationHandler struct {
	db *storage.Postgres
}

func NewIdentityMigrationHandler(db *storage.Postgres) *IdentityMigrationHandler {
	return &IdentityMigrationHandler{db: db}
}

const migrationPageSize = 100

// LegacyKey returns this device's copy of the legacy user key.
func (h *IdentityMigrationHandler) LegacyKey(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	envelope, err := h.db.GetLegacyUserKeyEnvelope(c.Request.Context(), user, c.Query("device_id"))
	if err != nil {
		writeLegacyLookupError(c, err)
		return
	}
	c.JSON(http.StatusOK, models.LegacyUserKeyResponse{
		WrappedUKB64: base64.StdEncoding.EncodeToString(envelope.WrappedUserKey),
		UKWrapAlg:    envelope.UKWrapAlg,
	})
}

// Start creates a legacy account's identity key from a device holding its
// legacy user key.
func (h *IdentityMigrationHandler) Start(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	var req models.StartIdentityMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}
	ctx := c.Request.Context()
	if _, err := h.db.GetLegacyUserKeyEnvelope(ctx, user, req.DeviceID); err != nil {
		writeLegacyLookupError(c, err)
		return
	}
	if _, err := h.db.GetActiveUserIdentityKey(ctx, user); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: "The account already has an identity key", Code: "IDENTITY_KEY_EXISTS"})
		return
	} else if !errors.Is(err, models.ErrIdentityKeyNotFound) {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to start migration", Code: "IDENTITY_MIGRATION_FAILED"})
		return
	}

	wrapped, err1 := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
	escrowed, err2 := base64.StdEncoding.DecodeString(req.EscrowWrappedPrivateKeyB64)
	nonce, err3 := base64.StdEncoding.DecodeString(req.EscrowNonceB64)
	if err1 != nil || err2 != nil || err3 != nil || len(wrapped) == 0 || len(escrowed) == 0 || len(nonce) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped identity key", Code: "INVALID_REQUEST"})
		return
	}
	algorithm := req.IdentityKeyAlgorithm
	if algorithm == "" {
		algorithm = "RSA-OAEP-2048"
	}
	now := time.Now()
	idKey := &models.UserIdentityKey{
		CNSUserID: user, KeyVersion: 1, PublicKeyJWK: req.IdentityPublicKeyJWK, KeyAlgorithm: algorithm,
		Status: "active", CreatedAt: now, ActivatedAt: sql.NullTime{Time: now, Valid: true},
	}
	envelope := &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: user, DeviceID: req.DeviceID, IdentityKeyVersion: 1, WrappedPrivateKey: wrapped,
		WrapAlg: req.IdentityKeyWrapAlg, WrapMeta: req.IdentityKeyWrapMeta, CreatedAt: now,
	}
	escrow := &models.LegacyIdentityEscrow{
		CNSUserID: user, IdentityKeyVersion: 1, WrappedPrivateKey: escrowed, WrapNonce: nonce, WrapAlg: req.EscrowWrapAlg,
	}
	if err := h.db.StartIdentityMigration(ctx, idKey, envelope, escrow); err != nil {
		// Another legacy device created the key first; this one adopts it.
		if _, getErr := h.db.GetActiveUserIdentityKey(ctx, user); getErr == nil {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: "The account already has an identity key", Code: "IDENTITY_KEY_EXISTS"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to start migration", Code: "IDENTITY_MIGRATION_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"identity_key_version": 1})
}

// Escrow returns the identity key encrypted with the legacy user key, to a
// device that holds the user key.
func (h *IdentityMigrationHandler) Escrow(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, err := h.db.GetLegacyUserKeyEnvelope(ctx, user, c.Query("device_id")); err != nil {
		writeLegacyLookupError(c, err)
		return
	}
	escrow, ok := h.activeEscrow(c, user)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, models.LegacyIdentityEscrowResponse{
		WrappedPrivateKeyB64: base64.StdEncoding.EncodeToString(escrow.WrappedPrivateKey),
		WrapNonceB64:         base64.StdEncoding.EncodeToString(escrow.WrapNonce),
		WrapAlg:              escrow.WrapAlg,
		IdentityKeyVersion:   escrow.IdentityKeyVersion,
	})
}

// Adopt stores a legacy device's own copy of the identity key, which it
// decrypted from the escrow.
func (h *IdentityMigrationHandler) Adopt(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	var req models.AdoptIdentityKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}
	ctx := c.Request.Context()
	if _, err := h.db.GetLegacyUserKeyEnvelope(ctx, user, req.DeviceID); err != nil {
		writeLegacyLookupError(c, err)
		return
	}
	escrow, ok := h.activeEscrow(c, user)
	if !ok {
		return
	}
	if req.IdentityKeyVersion != escrow.IdentityKeyVersion {
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: models.ErrIdentityKeyStale.Message, Code: models.ErrIdentityKeyStale.Code})
		return
	}
	if _, err := h.db.GetUserIdentityKeyDeviceEnvelope(ctx, user, req.DeviceID, escrow.IdentityKeyVersion); err == nil {
		c.JSON(http.StatusOK, gin.H{"stored": false})
		return
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
	if err != nil || len(wrapped) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped identity key", Code: "INVALID_REQUEST"})
		return
	}
	if err := h.db.CreateUserIdentityKeyDeviceEnvelope(ctx, &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: user, DeviceID: req.DeviceID, IdentityKeyVersion: escrow.IdentityKeyVersion,
		WrappedPrivateKey: wrapped, WrapAlg: req.IdentityKeyWrapAlg, WrapMeta: req.IdentityKeyWrapMeta,
		CreatedAt: time.Now(),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to store identity key", Code: "IDENTITY_MIGRATION_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"stored": true})
}

// Files lists the caller's uploads whose key is still only wrapped with the
// legacy user key, to a trusted device.
func (h *IdentityMigrationHandler) Files(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	if !trustedDevice(c, h.db, user, c.Query("device_id")) {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Trusted device approval is required", Code: "DEVICE_NOT_TRUSTED"})
		return
	}
	items, remaining, err := h.db.ListLegacyFilesToMigrate(c.Request.Context(), user, c.Query("after"), migrationPageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list files", Code: "IDENTITY_MIGRATION_FAILED"})
		return
	}
	resp := models.LegacyFileKeysResponse{Items: make([]models.LegacyFileKeyItem, 0, len(items)), Remaining: remaining}
	for _, item := range items {
		resp.Items = append(resp.Items, models.LegacyFileKeyItem{
			FileID:          item.FileID,
			WrappedDEKB64:   base64.StdEncoding.EncodeToString(item.WrappedDEK),
			DEKWrapAlg:      item.DEKWrapAlg,
			DEKWrapNonceB64: base64.StdEncoding.EncodeToString(item.DEKWrapNonce),
		})
	}
	c.JSON(http.StatusOK, resp)
}

// StoreFiles stores owner envelopes a trusted device re-wrapped from the
// legacy user key for the active identity key. Files that already have one
// are left alone.
func (h *IdentityMigrationHandler) StoreFiles(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	var req models.MigratedFileKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) > migrationPageSize {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST"})
		return
	}
	ctx := c.Request.Context()
	if !trustedDevice(c, h.db, user, req.DeviceID) {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Trusted device approval is required", Code: "DEVICE_NOT_TRUSTED"})
		return
	}
	active, err := h.db.GetActiveUserIdentityKey(ctx, user)
	if err != nil || active.KeyVersion != req.IdentityKeyVersion {
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: models.ErrIdentityKeyStale.Message, Code: models.ErrIdentityKeyStale.Code})
		return
	}
	envelopes, ok := decodeRewrappedKeys(c, req.Items)
	if !ok {
		return
	}
	stored, err := h.db.StoreMigratedOwnerEnvelopes(ctx, user, active.KeyVersion, envelopes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to store file keys", Code: "IDENTITY_MIGRATION_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"stored": stored})
}

// activeEscrow returns the account's escrow if it is for the active identity
// key; after a recovery it is stale and must not hand out an old key.
func (h *IdentityMigrationHandler) activeEscrow(c *gin.Context, user int64) (*models.LegacyIdentityEscrow, bool) {
	ctx := c.Request.Context()
	escrow, err := h.db.GetLegacyIdentityEscrow(ctx, user)
	if err == nil {
		var active *models.UserIdentityKey
		active, err = h.db.GetActiveUserIdentityKey(ctx, user)
		if err == nil && active.KeyVersion == escrow.IdentityKeyVersion {
			return escrow, true
		}
	}
	if err == nil || errors.Is(err, models.ErrIdentityKeyNotFound) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "No identity key to adopt", Code: "IDENTITY_ESCROW_NOT_FOUND"})
		return nil, false
	}
	c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load identity key", Code: "IDENTITY_MIGRATION_FAILED"})
	return nil, false
}

func writeLegacyLookupError(c *gin.Context, err error) {
	if errors.Is(err, models.ErrDeviceEnvelopeNotFound) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "This device holds no legacy user key", Code: "LEGACY_USER_KEY_NOT_FOUND"})
		return
	}
	c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to look up the legacy user key", Code: "IDENTITY_MIGRATION_FAILED"})
}
