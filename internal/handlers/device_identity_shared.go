package handlers

import (
	"encoding/base64"
	"net/http"
	"strings"

	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/services"

	"github.com/gin-gonic/gin"
)

func handleSharedDeviceRegistration(c *gin.Context, db services.DeviceStore, recover bool) {
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
	result, err := (&services.DeviceIdentity{DB: db}).Register(c.Request.Context(), int64(user.ID), req, recover)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusBadRequest
			if appErr == models.ErrDeviceNotAuthorized || appErr == models.ErrApproverNotTrusted {
				status = http.StatusForbidden
			}
			if appErr == models.ErrDeviceIDConflict {
				status = http.StatusConflict
			}
			c.JSON(status, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		if strings.HasPrefix(err.Error(), "invalid device public key") {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid device public key", Code: "INVALID_PUBLIC_KEY_JWK", Details: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to register device", Code: "DEVICE_REGISTER_FAILED"})
		return
	}
	response := models.DeviceRegisterResponse{DeviceID: result.DeviceID, NeedsEnrollment: result.NeedsEnrollment}
	if result.UserKeyEnvelope != nil {
		response.UserKeyEnvelope = &models.UserKeyEnvelopeResponse{
			WrappedUKB64: base64.StdEncoding.EncodeToString(result.UserKeyEnvelope.WrappedUserKey),
			UKWrapAlg:    result.UserKeyEnvelope.UKWrapAlg,
			UKWrapMeta:   result.UserKeyEnvelope.UKWrapMeta,
			KeyVersion:   result.UserKeyEnvelope.KeyVersion,
		}
	}
	if result.IdentityKeyEnvelope != nil {
		response.IdentityKeyEnvelope = &models.UserIdentityKeyDeviceEnvelopeResponse{
			WrappedPrivateKeyB64: base64.StdEncoding.EncodeToString(result.IdentityKeyEnvelope.WrappedPrivateKey),
			WrapAlg:              result.IdentityKeyEnvelope.WrapAlg,
			WrapMeta:             result.IdentityKeyEnvelope.WrapMeta,
			IdentityKeyVersion:   result.IdentityKeyEnvelope.IdentityKeyVersion,
		}
	}
	if result.ActiveIdentityKey != nil {
		response.IdentityPublicKey = &models.IdentityPublicKeyResponse{
			KeyVersion:   result.ActiveIdentityKey.KeyVersion,
			KeyAlgorithm: result.ActiveIdentityKey.KeyAlgorithm,
			PublicKeyJWK: result.ActiveIdentityKey.PublicKeyJWK,
		}
	}
	for _, device := range result.DevicesMissingIdentityKey {
		response.DevicesMissingIdentityKey = append(response.DevicesMissingIdentityKey, models.DeviceMissingIdentityKey{
			DeviceID: device.ID, PublicKeyJWK: device.PublicKeyJWK,
		})
	}
	c.JSON(http.StatusOK, response)
}

// sharedDistributeIdentityKey stores identity key copies a trusted device
// wrapped for the account's devices that are missing one.
func sharedDistributeIdentityKey(c *gin.Context, db services.DeviceStore) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	var req models.DistributeIdentityKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}
	stored, err := (&services.DeviceIdentity{DB: db}).DistributeIdentityKey(c.Request.Context(), int64(user.ID), req)
	if err != nil {
		writeDeviceServiceError(c, err, "IDENTITY_KEY_DISTRIBUTE_FAILED")
		return
	}
	c.JSON(http.StatusOK, gin.H{"stored": stored})
}

func trustedDevice(ctx *gin.Context, db services.DeviceStore, userID int64, deviceID string) bool {
	ok, err := (&services.DeviceIdentity{DB: db}).IsTrusted(ctx.Request.Context(), userID, deviceID)
	return err == nil && ok
}

func sharedCreateEnrollment(c *gin.Context, db services.DeviceStore) *models.DeviceEnrollment {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return nil
	}
	var req models.CreateEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return nil
	}
	item, err := (&services.DeviceIdentity{DB: db}).CreateEnrollment(c.Request.Context(), int64(user.ID), req.RequestDeviceID)
	if err != nil {
		writeDeviceServiceError(c, err, "ENROLLMENT_CREATE_FAILED")
		return nil
	}
	c.JSON(http.StatusOK, models.CreateEnrollmentResponse{
		EnrollmentID: item.ID, VerificationCode: item.VerificationCode, ExpiresAt: item.ExpiresAt,
	})
	return item
}

func sharedListEnrollments(c *gin.Context, db services.DeviceStore) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	items, err := (&services.DeviceIdentity{DB: db}).ListPending(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list enrollments", Code: "ENROLLMENT_LIST_FAILED"})
		return
	}
	c.JSON(http.StatusOK, models.PendingEnrollmentsResponse{Items: items})
}

func sharedApproveEnrollment(c *gin.Context, db services.DeviceStore) bool {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return false
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing enrollment id", Code: "INVALID_REQUEST"})
		return false
	}
	var req models.ApproveEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return false
	}
	if err := (&services.DeviceIdentity{DB: db}).Approve(c.Request.Context(), int64(user.ID), id, req); err != nil {
		writeDeviceServiceError(c, err, "ENROLLMENT_APPROVE_FAILED")
		return false
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
	return true
}

func sharedRejectEnrollment(c *gin.Context, db services.DeviceStore) bool {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return false
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing enrollment id", Code: "INVALID_REQUEST"})
		return false
	}
	var req models.RejectEnrollmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return false
	}
	if err := (&services.DeviceIdentity{DB: db}).Reject(c.Request.Context(), int64(user.ID), id, req.ApproverDeviceID); err != nil {
		writeDeviceServiceError(c, err, "ENROLLMENT_REJECT_FAILED")
		return false
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
	return true
}

func writeDeviceServiceError(c *gin.Context, err error, fallbackCode string) {
	if appErr, ok := err.(*models.AppError); ok {
		status := http.StatusBadRequest
		if appErr == models.ErrDeviceNotAuthorized || appErr == models.ErrApproverNotTrusted || appErr == models.ErrIdentityKeyNotHeld {
			status = http.StatusForbidden
		}
		c.JSON(status, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
		return
	}
	c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: err.Error(), Code: fallbackCode})
}
