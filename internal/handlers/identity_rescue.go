package handlers

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

// IdentityRescueHandler lets a device that still holds an identity key version
// from before the account's last recovery re-wrap the file keys locked to it
// for the active version. Any signed-in session may do so, since the rescuing
// device was revoked by the recovery. Only keys that are still locked can be
// replaced, so a bad upload can't break a key the user can open; but the
// rescuing browser wraps for the active public key as the server reports it,
// so a compromised server could collect the keys of locked files this way.
type IdentityRescueHandler struct {
	db *storage.Postgres
}

func NewIdentityRescueHandler(db *storage.Postgres) *IdentityRescueHandler {
	return &IdentityRescueHandler{db: db}
}

const rescuePageSize = 100

// Locked lists the caller's file keys wrapped for identity key ?version=,
// which must be older than the active one.
func (h *IdentityRescueHandler) Locked(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Query("version"))
	if err != nil || version <= 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "version is required", Code: "INVALID_REQUEST"})
		return
	}
	items, remaining, err := h.db.ListLockedFileKeys(c.Request.Context(), user, version, c.Query("after"), rescuePageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list locked keys", Code: "IDENTITY_RESCUE_FAILED"})
		return
	}
	resp := models.LockedFileKeysResponse{Items: make([]models.LockedFileKeyItem, 0, len(items)), Remaining: remaining}
	for _, item := range items {
		resp.Items = append(resp.Items, models.LockedFileKeyItem{
			FileID:        item.FileID,
			WrappedDEKB64: base64.StdEncoding.EncodeToString(item.WrappedDEK),
			DEKWrapAlg:    item.DEKWrapAlg,
		})
	}
	c.JSON(http.StatusOK, resp)
}

// Rescue replaces locked file keys with ones re-wrapped for the active
// identity key version.
func (h *IdentityRescueHandler) Rescue(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	var req models.RescueFileKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) > rescuePageSize || req.FromVersion >= req.ToVersion {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST"})
		return
	}
	envelopes, ok := decodeRewrappedKeys(c, req.Items)
	if !ok {
		return
	}
	rescued, err := h.db.RescueLockedFileKeys(c.Request.Context(), user, req.FromVersion, req.ToVersion, envelopes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to store file keys", Code: "IDENTITY_RESCUE_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rescued": rescued})
}

func requireUser(c *gin.Context) (int64, bool) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return 0, false
	}
	return int64(user.ID), true
}

// decodeRewrappedKeys turns re-wrapped file keys from a request into
// envelopes; only RSA-OAEP wraps for the identity key are accepted.
func decodeRewrappedKeys(c *gin.Context, items []models.RewrappedFileKey) ([]models.FileAccessKeyEnvelope, bool) {
	envelopes := make([]models.FileAccessKeyEnvelope, 0, len(items))
	for _, item := range items {
		wrapped, err := base64.StdEncoding.DecodeString(item.IdentityWrappedDEKB64)
		alg := strings.TrimSpace(item.IdentityDEKWrapAlg)
		if err != nil || len(wrapped) == 0 || item.FileID == "" || !strings.HasPrefix(strings.ToUpper(alg), "RSA-OAEP") {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid file key", Code: "INVALID_REQUEST"})
			return nil, false
		}
		envelopes = append(envelopes, models.FileAccessKeyEnvelope{FileID: item.FileID, WrappedDEK: wrapped, DEKWrapAlg: alg})
	}
	return envelopes, true
}
