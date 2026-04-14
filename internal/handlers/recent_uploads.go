package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shareit/internal/config"
	"shareit/internal/middleware"
	"shareit/internal/models"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

type RecentUploadsHandler struct {
	cfg *config.Config
	db  *storage.Postgres
	hub *deviceEnrollmentHub
}

func NewRecentUploadsHandler(cfg *config.Config, db *storage.Postgres) *RecentUploadsHandler {
	return &RecentUploadsHandler{cfg: cfg, db: db, hub: newDeviceEnrollmentHub()}
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
	if err != nil {
		status := http.StatusInternalServerError
		if err == models.ErrFileNotFound || err == models.ErrFileExpired || err == models.ErrFileDeleted {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
		return
	}

	userEnvelope, err := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), deviceID)
	if err != nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "No key envelope for this device", Code: "DEVICE_NOT_AUTHORIZED"})
		return
	}

	resp := models.FileAccessResponse{
		File: *file.ToMetadata(),
		FileKeyEnvelope: models.FileKeyEnvelopeResponse{
			WrappedDEKB64:   base64.StdEncoding.EncodeToString(fileEnvelope.WrappedDEK),
			DEKWrapAlg:      fileEnvelope.DEKWrapAlg,
			DEKWrapVersion:  fileEnvelope.DEKWrapVersion,
			DEKWrapNonceB64: base64.StdEncoding.EncodeToString(fileEnvelope.DEKWrapNonce),
		},
		UserKeyEnvelope: models.UserKeyEnvelopeResponse{
			WrappedUKB64: base64.StdEncoding.EncodeToString(userEnvelope.WrappedUserKey),
			UKWrapAlg:    userEnvelope.UKWrapAlg,
			UKWrapMeta:   userEnvelope.UKWrapMeta,
			KeyVersion:   userEnvelope.KeyVersion,
		},
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	var req models.DeviceRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	keyVersion := req.KeyVersion
	if keyVersion <= 0 {
		keyVersion = 1
	}

	device := &models.UserDevice{
		ID:           req.DeviceID,
		CNSUserID:    int64(user.ID),
		DeviceLabel:  req.DeviceLabel,
		PublicKeyJWK: req.PublicKeyJWK,
		KeyAlgorithm: req.KeyAlgorithm,
		KeyVersion:   keyVersion,
	}

	if forceRecovery {
		if req.WrappedUserKeyB64 == "" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Wrapped user key is required for device recovery", Code: "WRAPPED_UK_REQUIRED"})
			return
		}

		wrappedUserKey, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped user key", Code: "INVALID_WRAPPED_UK", Details: err.Error()})
			return
		}

		envelope := &models.UserKeyEnvelope{
			CNSUserID:      int64(user.ID),
			DeviceID:       req.DeviceID,
			WrappedUserKey: wrappedUserKey,
			UKWrapAlg:      req.UKWrapAlg,
			UKWrapMeta:     req.UKWrapMeta,
			KeyVersion:     keyVersion,
		}
		if err := h.db.ResetTrustedDeviceState(c.Request.Context(), device, envelope); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to reset trusted devices", Code: "DEVICE_RECOVERY_FAILED"})
			return
		}
		c.JSON(http.StatusOK, models.DeviceRegisterResponse{
			DeviceID:        req.DeviceID,
			NeedsEnrollment: false,
			UserKeyEnvelope: &models.UserKeyEnvelopeResponse{
				WrappedUKB64: base64.StdEncoding.EncodeToString(envelope.WrappedUserKey),
				UKWrapAlg:    envelope.UKWrapAlg,
				UKWrapMeta:   envelope.UKWrapMeta,
				KeyVersion:   envelope.KeyVersion,
			},
		})
		return
	}

	if err := h.db.CreateOrUpdateUserDevice(c.Request.Context(), device); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to register device", Code: "DEVICE_REGISTER_FAILED"})
		return
	}

	existingEnvelope, existingErr := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), req.DeviceID)
	if existingErr == nil {
		c.JSON(http.StatusOK, models.DeviceRegisterResponse{
			DeviceID:        req.DeviceID,
			NeedsEnrollment: false,
			UserKeyEnvelope: &models.UserKeyEnvelopeResponse{
				WrappedUKB64: base64.StdEncoding.EncodeToString(existingEnvelope.WrappedUserKey),
				UKWrapAlg:    existingEnvelope.UKWrapAlg,
				UKWrapMeta:   existingEnvelope.UKWrapMeta,
				KeyVersion:   existingEnvelope.KeyVersion,
			},
		})
		return
	}

	hasTrustedEnvelope, err := h.db.UserHasTrustedKeyEnvelope(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to inspect trusted devices", Code: "DEVICE_REGISTER_FAILED"})
		return
	}

	if hasTrustedEnvelope {
		c.JSON(http.StatusOK, models.DeviceRegisterResponse{DeviceID: req.DeviceID, NeedsEnrollment: true})
		return
	}

	if req.WrappedUserKeyB64 == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Wrapped user key is required for first trusted device", Code: "WRAPPED_UK_REQUIRED"})
		return
	}

	wrappedUserKey, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped user key", Code: "INVALID_WRAPPED_UK", Details: err.Error()})
		return
	}

	envelope := &models.UserKeyEnvelope{
		CNSUserID:      int64(user.ID),
		DeviceID:       req.DeviceID,
		WrappedUserKey: wrappedUserKey,
		UKWrapAlg:      req.UKWrapAlg,
		UKWrapMeta:     req.UKWrapMeta,
		KeyVersion:     keyVersion,
	}

	if err := h.db.SaveUserKeyEnvelope(c.Request.Context(), envelope); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to store user key envelope", Code: "SAVE_UK_ENVELOPE_FAILED"})
		return
	}

	c.JSON(http.StatusOK, models.DeviceRegisterResponse{
		DeviceID:        req.DeviceID,
		NeedsEnrollment: false,
		UserKeyEnvelope: &models.UserKeyEnvelopeResponse{
			WrappedUKB64: base64.StdEncoding.EncodeToString(envelope.WrappedUserKey),
			UKWrapAlg:    envelope.UKWrapAlg,
			UKWrapMeta:   envelope.UKWrapMeta,
			KeyVersion:   envelope.KeyVersion,
		},
	})
}

func (h *RecentUploadsHandler) CreateEnrollment(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	var req models.CreateEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	owned, err := h.userOwnsDevice(c.Request.Context(), int64(user.ID), req.RequestDeviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify requesting device", Code: "DEVICE_LOOKUP_FAILED"})
		return
	}
	if !owned {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Request device does not belong to user", Code: "DEVICE_NOT_AUTHORIZED"})
		return
	}

	enrollment := &models.DeviceEnrollment{
		CNSUserID:        int64(user.ID),
		RequestDeviceID:  req.RequestDeviceID,
		VerificationCode: generateVerificationCode(6),
		Status:           models.EnrollmentStatusPending,
		ExpiresAt:        time.Now().Add(10 * time.Minute),
	}
	if err := h.db.CreateEnrollmentRequest(c.Request.Context(), enrollment); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create enrollment", Code: "ENROLLMENT_CREATE_FAILED"})
		return
	}
	h.publishEnrollmentChange(c.Request.Context(), int64(user.ID), "device_enrollment_created", enrollment.ID, "")

	c.JSON(http.StatusOK, models.CreateEnrollmentResponse{
		EnrollmentID:     enrollment.ID,
		VerificationCode: enrollment.VerificationCode,
		ExpiresAt:        enrollment.ExpiresAt,
	})
}

func (h *RecentUploadsHandler) ListPendingEnrollments(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	_ = h.db.TouchExpiredEnrollments(c.Request.Context(), int64(user.ID))
	items, err := h.db.ListPendingEnrollments(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list enrollments", Code: "ENROLLMENT_LIST_FAILED"})
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list devices", Code: "DEVICE_LIST_FAILED"})
		return
	}
	deviceByID := make(map[string]models.UserDevice, len(devices))
	for _, device := range devices {
		deviceByID[device.ID] = device
	}

	respItems := make([]models.PendingEnrollmentItem, 0, len(items))
	for _, item := range items {
		device, ok := deviceByID[item.RequestDeviceID]
		if !ok {
			device = models.UserDevice{ID: item.RequestDeviceID}
		}
		respItems = append(respItems, models.PendingEnrollmentItem{
			Enrollment:    item,
			RequestDevice: device,
		})
	}

	c.JSON(http.StatusOK, models.PendingEnrollmentsResponse{Items: respItems})
}

func (h *RecentUploadsHandler) ApproveEnrollment(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	enrollmentID := c.Param("id")
	if enrollmentID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing enrollment id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.ApproveEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	owned, err := h.userOwnsDevice(c.Request.Context(), int64(user.ID), req.ApproverDeviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify approver device", Code: "DEVICE_LOOKUP_FAILED"})
		return
	}
	if !owned {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Approver device does not belong to user", Code: "DEVICE_NOT_AUTHORIZED"})
		return
	}

	if _, err := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), req.ApproverDeviceID); err != nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Approver device is not trusted", Code: "APPROVER_NOT_TRUSTED"})
		return
	}

	enrollment, err := h.db.GetEnrollmentByID(c.Request.Context(), int64(user.ID), enrollmentID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Enrollment not found", Code: "ENROLLMENT_NOT_FOUND"})
		return
	}

	if enrollment.Status != models.EnrollmentStatusPending || time.Now().After(enrollment.ExpiresAt) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Enrollment is no longer pending", Code: "ENROLLMENT_NOT_PENDING"})
		return
	}

	if !strings.EqualFold(strings.TrimSpace(req.VerificationCode), strings.TrimSpace(enrollment.VerificationCode)) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Verification code mismatch", Code: "VERIFICATION_CODE_MISMATCH"})
		return
	}

	wrappedUserKey, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped user key", Code: "INVALID_WRAPPED_UK", Details: err.Error()})
		return
	}

	envelope := &models.UserKeyEnvelope{
		CNSUserID:      int64(user.ID),
		DeviceID:       enrollment.RequestDeviceID,
		WrappedUserKey: wrappedUserKey,
		UKWrapAlg:      req.UKWrapAlg,
		UKWrapMeta:     req.UKWrapMeta,
		KeyVersion:     1,
	}
	if err := h.db.SaveUserKeyEnvelope(c.Request.Context(), envelope); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to persist wrapped user key", Code: "SAVE_UK_ENVELOPE_FAILED"})
		return
	}

	if err := h.db.ApproveEnrollment(c.Request.Context(), int64(user.ID), enrollmentID, req.ApproverDeviceID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to approve enrollment", Code: "ENROLLMENT_APPROVE_FAILED"})
		return
	}
	h.publishEnrollmentChange(c.Request.Context(), int64(user.ID), "device_enrollment_approved", enrollmentID, req.ApproverDeviceID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *RecentUploadsHandler) RejectEnrollment(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	enrollmentID := c.Param("id")
	if enrollmentID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing enrollment id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.RejectEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	owned, err := h.userOwnsDevice(c.Request.Context(), int64(user.ID), req.ApproverDeviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify approver device", Code: "DEVICE_LOOKUP_FAILED"})
		return
	}
	if !owned {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Approver device does not belong to user", Code: "DEVICE_NOT_AUTHORIZED"})
		return
	}

	if _, err := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), req.ApproverDeviceID); err != nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Approver device is not trusted", Code: "APPROVER_NOT_TRUSTED"})
		return
	}

	if err := h.db.RejectEnrollment(c.Request.Context(), int64(user.ID), enrollmentID); err != nil {
		if err == models.ErrUploadNotPending {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Enrollment is no longer pending", Code: "ENROLLMENT_NOT_PENDING"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to reject enrollment", Code: "ENROLLMENT_REJECT_FAILED"})
		return
	}
	h.publishEnrollmentChange(c.Request.Context(), int64(user.ID), "device_enrollment_rejected", enrollmentID, "")

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *RecentUploadsHandler) userOwnsDevice(ctx context.Context, userID int64, deviceID string) (bool, error) {
	devices, err := h.db.GetActiveDevicesByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, d := range devices {
		if d.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
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
