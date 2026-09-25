package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"sendly/internal/config"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

type RecentUploadsHandler struct {
	cfg        *config.Config
	db         *storage.Postgres
	hub        *deviceEnrollmentHub
	androidHub *androidHub
}

func NewRecentUploadsHandler(cfg *config.Config, db *storage.Postgres) *RecentUploadsHandler {
	return &RecentUploadsHandler{cfg: cfg, db: db, hub: newDeviceEnrollmentHub()}
}

func (h *RecentUploadsHandler) SetAndroidHub(ah *androidHub) {
	h.androidHub = ah
}

func (h *RecentUploadsHandler) RecentUploads(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage < 1 {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid page parameter", Code: "INVALID_PAGINATION"})
			return
		}
		page = parsedPage
	}

	perPage := 10
	if rawPerPage := strings.TrimSpace(c.Query("per_page")); rawPerPage != "" {
		parsedPerPage, err := strconv.Atoi(rawPerPage)
		if err != nil || parsedPerPage < 1 {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid per_page parameter", Code: "INVALID_PAGINATION"})
			return
		}
		if parsedPerPage > 50 {
			parsedPerPage = 50
		}
		perPage = parsedPerPage
	}

	searchQuery := strings.TrimSpace(c.Query("q"))

	items, total, err := h.db.GetOwnedRecentFiles(c.Request.Context(), int64(user.ID), searchQuery, page, perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch recent uploads", Code: "RECENT_UPLOADS_FAILED"})
		return
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}

	if totalPages > 0 && page > totalPages {
		page = totalPages
		items, total, err = h.db.GetOwnedRecentFiles(c.Request.Context(), int64(user.ID), searchQuery, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch recent uploads", Code: "RECENT_UPLOADS_FAILED"})
			return
		}
		totalPages = (total + perPage - 1) / perPage
	}

	for i := range items {
		items[i].ShareURL = h.cfg.BaseURL + "/shared/" + items[i].FileID
	}

	c.JSON(http.StatusOK, models.RecentUploadsResponse{
		Items:      items,
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		Query:      searchQuery,
	})
}

func (h *RecentUploadsHandler) FileAccess(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	fileID := c.Param("id")
	deviceID := c.Query("device_id")
	if fileID == "" || deviceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "file id and device_id are required", Code: "INVALID_REQUEST"})
		return
	}

	file, fileEnvelope, err := h.db.GetOwnedFileWithEnvelope(c.Request.Context(), int64(user.ID), fileID)
	identityOnly := false
	if err != nil {
		file, fileEnvelope, err = h.db.GetTunnelRecipientFileWithEnvelope(c.Request.Context(), int64(user.ID), deviceID, fileID)
		if err != nil {
			if grant, grantErr := h.db.GetFileAccessKeyEnvelope(c.Request.Context(), fileID, int64(user.ID)); grantErr == nil && grant.AccessKind == "share" {
				file, err = h.db.GetFileByID(c.Request.Context(), fileID)
				if err == nil {
					fileEnvelope = &models.FileKeyEnvelope{}
					identityOnly = true
				}
			}
			if err != nil || file == nil {
				status := http.StatusInternalServerError
				if err == models.ErrFileNotFound || err == models.ErrFileExpired || err == models.ErrFileDeleted {
					status = http.StatusNotFound
				}
				c.JSON(status, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
				return
			}
		}
	}

	resp := models.FileAccessResponse{
		File: *file.ToMetadata(),
		FileKeyEnvelope: models.FileKeyEnvelopeResponse{
			WrappedDEKB64:   base64.StdEncoding.EncodeToString(fileEnvelope.WrappedDEK),
			DEKWrapAlg:      fileEnvelope.DEKWrapAlg,
			DEKWrapVersion:  fileEnvelope.DEKWrapVersion,
			DEKWrapNonceB64: base64.StdEncoding.EncodeToString(fileEnvelope.DEKWrapNonce),
		},
	}
	if identityEnvelope, identityErr := h.db.GetFileAccessKeyEnvelope(c.Request.Context(), fileID, int64(user.ID)); identityErr == nil {
		resp.IdentityFileAccessEnvelope = &models.FileKeyEnvelopeResponse{
			WrappedDEKB64:   base64.StdEncoding.EncodeToString(identityEnvelope.WrappedDEK),
			DEKWrapAlg:      identityEnvelope.DEKWrapAlg,
			DEKWrapVersion:  identityEnvelope.DEKWrapVersion,
			DEKWrapNonceB64: base64.StdEncoding.EncodeToString(identityEnvelope.DEKWrapNonce),
		}
	}

	isDirectDeviceWrap := identityOnly || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(fileEnvelope.DEKWrapAlg)), "RSA-OAEP")
	if !isDirectDeviceWrap {
		userEnvelope, userErr := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), deviceID)
		if userErr != nil {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "No key envelope for this device", Code: "DEVICE_NOT_AUTHORIZED"})
			return
		}

		resp.UserKeyEnvelope = models.UserKeyEnvelopeResponse{
			WrappedUKB64: base64.StdEncoding.EncodeToString(userEnvelope.WrappedUserKey),
			UKWrapAlg:    userEnvelope.UKWrapAlg,
			UKWrapMeta:   userEnvelope.UKWrapMeta,
			KeyVersion:   userEnvelope.KeyVersion,
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (h *RecentUploadsHandler) RegisterDevice(c *gin.Context) {
	h.handleDeviceRegistration(c, false)
}

func (h *RecentUploadsHandler) RecoverDevice(c *gin.Context) {
	h.handleDeviceRegistration(c, true)
}

func (h *RecentUploadsHandler) handleDeviceRegistration(c *gin.Context, forceRecovery bool) {
	handleSharedDeviceRegistration(c, h.db, forceRecovery)
	return
}

func (h *RecentUploadsHandler) CreateEnrollment(c *gin.Context) {
	sharedCreateEnrollment(c, h.db)
	return
}

func (h *RecentUploadsHandler) ListPendingEnrollments(c *gin.Context) {
	sharedListEnrollments(c, h.db)
	return
}

func (h *RecentUploadsHandler) ApproveEnrollment(c *gin.Context) {
	sharedApproveEnrollment(c, h.db)
	return
}

func (h *RecentUploadsHandler) RejectEnrollment(c *gin.Context) {
	sharedRejectEnrollment(c, h.db)
	return
}

func (h *RecentUploadsHandler) DistributeIdentityKey(c *gin.Context) {
	sharedDistributeIdentityKey(c, h.db)
}

func generateVerificationCode(length int) string {
	if length <= 0 {
		length = 6
	}
	const digits = "0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			result[i] = digits[0]
			continue
		}
		result[i] = digits[n.Int64()]
	}
	return string(result)
}
