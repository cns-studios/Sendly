package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"shareit/internal/config"
	"shareit/internal/middleware"
	"shareit/internal/models"
	"shareit/internal/services"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var androidWSUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type androidHub struct {
	mu              sync.Mutex
	userConns       map[int64][]*websocket.Conn
	pendingConns    map[int64][]*websocket.Conn
	enrollmentConns map[string][]*websocket.Conn
}

func newAndroidHub() *androidHub {
	return &androidHub{
		userConns:       make(map[int64][]*websocket.Conn),
		pendingConns:    make(map[int64][]*websocket.Conn),
		enrollmentConns: make(map[string][]*websocket.Conn),
	}
}

func (h *androidHub) addUser(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.userConns[userID] = append(h.userConns[userID], conn)
}

func (h *androidHub) addPending(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pendingConns[userID] = append(h.pendingConns[userID], conn)
}

func (h *androidHub) addEnrollment(enrollmentID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enrollmentConns[enrollmentID] = append(h.enrollmentConns[enrollmentID], conn)
}

func (h *androidHub) broadcastUser(userID int64, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.userConns[userID]
	alive := conns[:0]
	for _, conn := range conns {
		if err := conn.WriteJSON(payload); err != nil {
			_ = conn.Close()
			continue
		}
		alive = append(alive, conn)
	}
	h.userConns[userID] = alive
}

func (h *androidHub) broadcastPending(userID int64, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.pendingConns[userID]
	alive := conns[:0]
	for _, conn := range conns {
		if err := conn.WriteJSON(payload); err != nil {
			_ = conn.Close()
			continue
		}
		alive = append(alive, conn)
	}
	h.pendingConns[userID] = alive
}

func (h *androidHub) broadcastEnrollment(enrollmentID string, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.enrollmentConns[enrollmentID]
	alive := conns[:0]
	for _, conn := range conns {
		if err := conn.WriteJSON(payload); err != nil {
			_ = conn.Close()
			continue
		}
		alive = append(alive, conn)
	}
	h.enrollmentConns[enrollmentID] = alive
}

type AndroidHandler struct {
	cfg           *config.Config
	db            *storage.Postgres
	fs            *storage.Filesystem
	uploadService *services.Upload
	hub           *androidHub
}

func NewAndroidHandler(
	cfg *config.Config,
	db *storage.Postgres,
	fs *storage.Filesystem,
	uploadService *services.Upload,
) *AndroidHandler {
	return &AndroidHandler{
		cfg:           cfg,
		db:            db,
		fs:            fs,
		uploadService: uploadService,
		hub:           newAndroidHub(),
	}
}

func (h *AndroidHandler) ListFiles(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	items, _, err := h.db.GetOwnedRecentFiles(c.Request.Context(), int64(user.ID), "", 1, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list files", Code: "LIST_FAILED"})
		return
	}
	if items == nil {
		items = []models.OwnedFileListItem{}
	}

	for i := range items {
		items[i].ShareURL = h.cfg.BaseURL + "/shared/" + items[i].FileID
	}

	c.JSON(http.StatusOK, items)
}

func (h *AndroidHandler) GetFile(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	fileID := c.Param("id")
	if !isAndroidFileID(fileID) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid file ID format", Code: "INVALID_FILE_ID"})
		return
	}

	file, _, err := h.db.GetOwnedFileWithEnvelope(c.Request.Context(), int64(user.ID), fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
		return
	}

	c.JSON(http.StatusOK, file.ToMetadata())
}

func (h *AndroidHandler) Download(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	fileID := c.Param("id")
	if !isAndroidFileID(fileID) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid file ID format", Code: "INVALID_FILE_ID"})
		return
	}

	file, _, err := h.db.GetOwnedFileWithEnvelope(c.Request.Context(), int64(user.ID), fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
		return
	}

	reader, err := h.fs.GetFileReader(fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "File not on storage", Code: "FILE_NOT_ON_DISK"})
		return
	}
	defer reader.Close()

	fileSize, _ := h.fs.GetFileSize(fileID)
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.enc\"", fileID))
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Length", fmt.Sprintf("%d", fileSize))
	c.Header("X-Original-Filename", file.OriginalName)
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

func (h *AndroidHandler) UploadInit(c *gin.Context) {
	var req models.UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	tier := middleware.GetTier(h.cfg, middleware.GetCNSUser(c))
	if req.FileSize > tier.MaxFileSize {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrFileTooLarge.Message, Code: models.ErrFileTooLarge.Code})
		return
	}

	resp, err := h.uploadService.InitUpload(c.Request.Context(), &req, middleware.GetClientIP(c))
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to initialize upload", Code: "UPLOAD_INIT_FAILED"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *AndroidHandler) UploadChunk(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Failed to parse multipart form", Code: "PARSE_ERROR", Details: err.Error()})
		return
	}

	sessionID := c.PostForm("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing session_id", Code: "MISSING_SESSION_ID"})
		return
	}

	chunkIndexStr := c.PostForm("chunk_index")
	if chunkIndexStr == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing chunk_index", Code: "MISSING_CHUNK_INDEX"})
		return
	}

	var chunkIndex int
	if _, err := fmt.Sscanf(chunkIndexStr, "%d", &chunkIndex); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid chunk_index", Code: "INVALID_CHUNK_INDEX", Details: err.Error()})
		return
	}

	file, _, err := c.Request.FormFile("chunk")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing chunk file", Code: "MISSING_CHUNK", Details: err.Error()})
		return
	}
	defer file.Close()

	if err := h.uploadService.UploadChunk(c.Request.Context(), sessionID, chunkIndex, file); err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to upload chunk", Code: "CHUNK_UPLOAD_FAILED"})
		return
	}

	uploaded, total, err := h.uploadService.GetUploadProgress(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "chunk_index": chunkIndex})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "chunk_index": chunkIndex, "uploaded_chunks": uploaded, "total_chunks": total})
}

func (h *AndroidHandler) UploadComplete(c *gin.Context) {
	var req models.UploadCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request", Code: "INVALID_REQUEST"})
		return
	}
	if !req.Confirmed {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Upload must be confirmed", Code: "NOT_CONFIRMED"})
		return
	}

	resp, err := h.uploadService.CompleteUpload(c.Request.Context(), req.SessionID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Complete failed", Code: "COMPLETE_FAILED"})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *AndroidHandler) UploadFinalize(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	var req models.UploadFinalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request", Code: "INVALID_REQUEST"})
		return
	}
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tier := middleware.GetTier(h.cfg, user)
	if req.TunnelID == "" {
		if !tier.IsDurationAllowed(req.Duration) {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Duration not available for your account tier", Code: "DURATION_NOT_ALLOWED"})
			return
		}
	}

	if req.TunnelID == "" && req.Duration == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Duration is required for non-tunnel uploads", Code: "DURATION_REQUIRED"})
		return
	}

	var opts *services.FinalizeUploadOptions
	if req.TunnelID != "" {
		uid := int64(user.ID)
		uname := user.Username
		opts = &services.FinalizeUploadOptions{OwnerCNSUserID: &uid, OwnerCNSUserName: &uname}

		if req.DeviceID == "" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "device_id is required for authenticated uploads", Code: "DEVICE_ID_REQUIRED"})
			return
		}

		if _, trustedErr := h.db.GetUserKeyEnvelopeForDevice(c.Request.Context(), int64(user.ID), req.DeviceID); trustedErr != nil {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Trusted device approval is required before authenticated uploads can be finalized", Code: "DEVICE_NOT_TRUSTED"})
			return
		}

		if req.WrappedDEKB64 != "" {
			wrappedDEK, decodeErr := base64.StdEncoding.DecodeString(req.WrappedDEKB64)
			if decodeErr != nil {
				c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped DEK", Code: "INVALID_WRAPPED_DEK", Details: decodeErr.Error()})
				return
			}
			opts.WrappedDEK = wrappedDEK
			opts.DEKWrapAlg = req.DEKWrapAlg
			opts.DEKWrapVersion = req.DEKWrapVersion
			if req.DEKWrapNonceB64 != "" {
				nonce, nonceErr := base64.StdEncoding.DecodeString(req.DEKWrapNonceB64)
				if nonceErr != nil {
					c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid DEK wrap nonce", Code: "INVALID_DEK_WRAP_NONCE", Details: nonceErr.Error()})
					return
				}
				opts.DEKWrapNonce = nonce
			}
		}

		tunnel, err := h.db.GetTunnelByID(c.Request.Context(), req.TunnelID)
		if err != nil {
			status := http.StatusBadRequest
			if err == models.ErrFileExpired {
				status = http.StatusGone
			}
			c.JSON(status, models.ErrorResponse{Error: "Tunnel is no longer available", Code: "TUNNEL_NOT_AVAILABLE"})
			return
		}
		if tunnel.Status != models.TunnelStatusActive {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel is not active", Code: "TUNNEL_NOT_ACTIVE"})
			return
		}
		if ok, _ := h.db.TunnelBelongsToUser(c.Request.Context(), req.TunnelID, int64(user.ID)); !ok {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel does not belong to this account", Code: "TUNNEL_FORBIDDEN"})
			return
		}

		if peerUserID, peerDeviceID := resolveTunnelPeerRecipient(tunnel, int64(user.ID)); peerUserID != 0 {
			peerEnvelope, peerErr := buildRecipientEnvelopeFromRequest(
				req.SessionID,
				peerUserID,
				peerDeviceID,
				req.PeerWrappedDEKB64,
				req.PeerDEKWrapAlg,
				req.PeerDEKWrapNonceB64,
				req.PeerDEKWrapVersion,
			)
			if peerErr != nil {
				c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Cross-account tunnel upload requires a peer key envelope", Code: "PEER_WRAPPED_DEK_REQUIRED", Details: peerErr.Error()})
				return
			}
			opts.RecipientEnvelopes = append(opts.RecipientEnvelopes, peerEnvelope)
		}
		opts.TunnelID = req.TunnelID
		opts.TunnelExpiresAt = tunnel.ExpiresAt
	}

	resp, err := h.uploadService.FinalizeUploadWithOptions(c.Request.Context(), req.SessionID, req.Duration, opts)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Finalize failed", Code: "FINALIZE_FAILED"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *AndroidHandler) ListConnectedDevices(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list devices", Code: "DEVICE_LIST_FAILED"})
		return
	}

	sort.SliceStable(devices, func(i, j int) bool {
		return devices[i].CreatedAt.Before(devices[j].CreatedAt)
	})

	c.JSON(http.StatusOK, devices)
}

func (h *AndroidHandler) RenameDevice(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	deviceID := c.Param("id")
	if deviceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing device id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.DeviceRenameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	if err := h.db.UpdateUserDeviceLabel(c.Request.Context(), int64(user.ID), deviceID, strings.TrimSpace(req.DeviceLabel)); err != nil {
		if err == models.ErrDeviceNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Device not found", Code: "DEVICE_NOT_FOUND"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to rename device", Code: "DEVICE_RENAME_FAILED"})
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}
	for _, device := range devices {
		if device.ID == deviceID {
			c.JSON(http.StatusOK, gin.H{"success": true, "device": device})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AndroidHandler) RegisterDevice(c *gin.Context) {
	h.handleDeviceRegistration(c, false)
}

func (h *AndroidHandler) RecoverDevice(c *gin.Context) {
	h.handleDeviceRegistration(c, true)
}

func (h *AndroidHandler) handleDeviceRegistration(c *gin.Context, forceRecovery bool) {
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

func (h *AndroidHandler) CreateEnrollment(c *gin.Context) {
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
		VerificationCode: generateAndroidVerificationCode(6),
		Status:           models.EnrollmentStatusPending,
		ExpiresAt:        time.Now().Add(10 * time.Minute),
	}
	if err := h.db.CreateEnrollmentRequest(c.Request.Context(), enrollment); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create enrollment", Code: "ENROLLMENT_CREATE_FAILED"})
		return
	}

	h.publishEnrollmentChange(c.Request.Context(), int64(user.ID), "device_enrollment_created", enrollment.ID, "")
	c.JSON(http.StatusOK, models.CreateEnrollmentResponse{EnrollmentID: enrollment.ID, VerificationCode: enrollment.VerificationCode, ExpiresAt: enrollment.ExpiresAt})
}

func (h *AndroidHandler) ListPendingEnrollments(c *gin.Context) {
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
		respItems = append(respItems, models.PendingEnrollmentItem{Enrollment: item, RequestDevice: device})
	}

	c.JSON(http.StatusOK, models.PendingEnrollmentsResponse{Items: respItems})
}

func (h *AndroidHandler) ApproveEnrollment(c *gin.Context) {
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

func (h *AndroidHandler) RejectEnrollment(c *gin.Context) {
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

func (h *AndroidHandler) DeviceNotificationsWS(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	conn, err := androidWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.hub.addUser(int64(user.ID), conn)
	go holdOpen(conn)
}

func (h *AndroidHandler) PendingApprovalsWS(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	conn, err := androidWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.hub.addPending(int64(user.ID), conn)
	pendingItems, _ := h.db.ListPendingEnrollments(c.Request.Context(), int64(user.ID))
	_ = conn.WriteJSON(gin.H{"type": "pending_approvals_snapshot", "pending_count": len(pendingItems)})
	go holdOpen(conn)
}

func (h *AndroidHandler) WaitingForApprovalWS(c *gin.Context) {
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

	enrollment, err := h.db.GetEnrollmentByID(c.Request.Context(), int64(user.ID), enrollmentID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Enrollment not found", Code: "ENROLLMENT_NOT_FOUND"})
		return
	}

	conn, err := androidWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.hub.addEnrollment(enrollmentID, conn)
	_ = conn.WriteJSON(gin.H{
		"type":              "enrollment_status_snapshot",
		"enrollment_id":     enrollmentID,
		"status":            enrollment.Status,
		"expires_at":        enrollment.ExpiresAt,
		"request_device_id": enrollment.RequestDeviceID,
	})
	go holdOpen(conn)
}

func (h *AndroidHandler) publishEnrollmentChange(ctx context.Context, userID int64, eventType, enrollmentID, approverDeviceID string) {
	enrollment, err := h.db.GetEnrollmentByID(ctx, userID, enrollmentID)
	if err != nil {
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(ctx, userID)
	requestDevice := models.UserDevice{ID: enrollment.RequestDeviceID}
	if err == nil {
		for _, device := range devices {
			if device.ID == enrollment.RequestDeviceID {
				requestDevice = device
				break
			}
		}
	}

	pendingItems, err := h.db.ListPendingEnrollments(ctx, userID)
	pendingCount := 0
	if err == nil {
		pendingCount = len(pendingItems)
	}

	payload := gin.H{
		"type":               eventType,
		"enrollment":         enrollment,
		"request_device":     requestDevice,
		"approver_device_id": approverDeviceID,
		"pending_count":      pendingCount,
	}
	h.hub.broadcastUser(userID, payload)
	h.hub.broadcastPending(userID, gin.H{"type": "pending_approvals_updated", "pending_count": pendingCount, "enrollment": enrollment, "request_device": requestDevice})
	h.hub.broadcastEnrollment(enrollmentID, gin.H{"type": "enrollment_status", "enrollment": enrollment, "approver_device_id": approverDeviceID})
}

func (h *AndroidHandler) userOwnsDevice(ctx context.Context, userID int64, deviceID string) (bool, error) {
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

func holdOpen(conn *websocket.Conn) {
	defer conn.Close()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func isAndroidFileID(id string) bool {
	if len(id) != 17 {
		return false
	}
	matched, _ := regexp.MatchString("^[a-z0-9]+$", id)
	return matched
}

func generateAndroidVerificationCode(length int) string {
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
