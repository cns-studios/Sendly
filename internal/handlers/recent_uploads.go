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

// FileAccess hands a signed-in user their identity-wrapped copy of a file
// key: their own upload's ('owner') or one sent to them in a transfer they
// accepted ('share'). The key can only be opened with the identity key
// version it was wrapped for.
func (h *RecentUploadsHandler) FileAccess(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	fileID := c.Param("id")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "file id is required", Code: "INVALID_REQUEST"})
		return
	}

	file, err := h.db.GetFileByID(c.Request.Context(), fileID)
	if err == nil {
		var envelope *models.FileAccessKeyEnvelope
		envelope, err = h.db.GetFileAccessKeyEnvelope(c.Request.Context(), fileID, int64(user.ID))
		if err == nil {
			c.JSON(http.StatusOK, models.FileAccessResponse{
				File: *file.ToMetadata(),
				FileAccessKeyEnvelope: models.FileKeyEnvelopeResponse{
					WrappedDEKB64:   base64.StdEncoding.EncodeToString(envelope.WrappedDEK),
					DEKWrapAlg:      envelope.DEKWrapAlg,
					DEKWrapVersion:  envelope.DEKWrapVersion,
					DEKWrapNonceB64: base64.StdEncoding.EncodeToString(envelope.DEKWrapNonce),
				},
				IdentityKeyVersion: envelope.RecipientKeyVersion,
				AccessKind:         envelope.AccessKind,
			})
			return
		}
	}

	status := http.StatusInternalServerError
	if err == models.ErrFileNotFound || err == models.ErrFileExpired || err == models.ErrFileDeleted || err == models.ErrFileAccessNotFound {
		status = http.StatusNotFound
	}
	c.JSON(status, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
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
