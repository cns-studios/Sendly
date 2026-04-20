package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"shareit/internal/config"
	"shareit/internal/middleware"
	"shareit/internal/models"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

type TunnelHandler struct {
	cfg *config.Config
	db  *storage.Postgres
	fs  *storage.Filesystem
}

func NewTunnelHandler(cfg *config.Config, db *storage.Postgres, fs *storage.Filesystem) *TunnelHandler {
	return &TunnelHandler{cfg: cfg, db: db, fs: fs}
}

func (h *TunnelHandler) Start(c *gin.Context) {
	var req models.TunnelStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	dur, err := models.ParseTunnelDuration(req.Duration)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid tunnel duration", Code: models.ErrInvalidDuration.Code})
		return
	}

	code, err := h.generateUniqueTunnelCode(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to generate tunnel code", Code: "TUNNEL_CODE_FAILED"})
		return
	}

	var initiatorUserID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		initiatorUserID = int64(user.ID)
	}

	tunnel := &models.Tunnel{
		Code:               code,
		InitiatorCNSUserID: initiatorUserID,
		InitiatorDeviceID:  nullableDeviceID(req.DeviceID),
		DurationMinutes:    int(dur.Minutes()),
		ExpiresAt:          time.Now().Add(dur),
	}
	if err := h.db.CreateTunnel(c.Request.Context(), tunnel); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create tunnel", Code: "TUNNEL_CREATE_FAILED"})
		return
	}

	c.JSON(http.StatusOK, models.TunnelStartResponse{
		Tunnel:    *tunnel,
		QRPayload: h.buildQRPayload(tunnel),
	})
}

func (h *TunnelHandler) Join(c *gin.Context) {
	var req models.TunnelJoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	tunnel, err := h.db.GetTunnelByCode(c.Request.Context(), req.Code)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	if tunnel.Status == models.TunnelStatusEnded || tunnel.Status == models.TunnelStatusExpired {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	var peerUserID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		peerUserID = int64(user.ID)
	}

	joined, joinErr := h.db.JoinTunnel(c.Request.Context(), tunnel.ID, peerUserID, req.DeviceID)
	if joinErr != nil {
		if joinErr == models.ErrFileExpired {
			c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel expired", Code: "TUNNEL_EXPIRED"})
			return
		}
		if joinErr == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to join tunnel", Code: "TUNNEL_JOIN_FAILED"})
		return
	}

	c.JSON(http.StatusOK, models.TunnelStartResponse{
		Tunnel:    *joined,
		QRPayload: h.buildQRPayload(joined),
	})
}

func (h *TunnelHandler) Confirm(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.TunnelConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	if _, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID); err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	var userID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		userID = int64(user.ID)
	}

	tunnel, err := h.db.ConfirmTunnel(c.Request.Context(), tunnelID, userID, req.DeviceID)
	if err != nil {
		if err == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
			return
		}
		if err == models.ErrFileExpired {
			c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel expired", Code: "TUNNEL_EXPIRED"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to confirm tunnel", Code: "TUNNEL_CONFIRM_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tunnel": tunnel})
}

func (h *TunnelHandler) End(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	fileIDs, err := h.db.GetTunnelFileIDs(c.Request.Context(), tunnelID)
	if err == nil {
		for _, fileID := range fileIDs {
			_ = h.fs.DeleteFile(fileID)
		}
	}

	if err := h.db.DeleteTunnel(c.Request.Context(), tunnelID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to end tunnel", Code: "TUNNEL_END_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *TunnelHandler) Get(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	files, err := h.db.GetTunnelFiles(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load tunnel files", Code: "TUNNEL_FILES_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tunnel": tunnel,
		"files":  files,
	})
}

func (h *TunnelHandler) Files(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	files, err := h.db.GetTunnelFiles(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load tunnel files", Code: "TUNNEL_FILES_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": files})
}

func (h *TunnelHandler) PeerWrapKey(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	if tunnel.Status != models.TunnelStatusActive {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel is not active", Code: "TUNNEL_NOT_ACTIVE"})
		return
	}

	peerUserID, peerDeviceID := resolveTunnelPeerRecipient(tunnel, int64(user.ID))
	if peerUserID == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Peer key sharing is only needed for cross-account tunnels", Code: "PEER_KEY_NOT_REQUIRED"})
		return
	}

	if strings.TrimSpace(peerDeviceID) == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Peer device is not ready for key sharing", Code: "PEER_DEVICE_NOT_READY"})
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(c.Request.Context(), peerUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to inspect peer device", Code: "PEER_DEVICE_LOOKUP_FAILED"})
		return
	}

	for _, device := range devices {
		if strings.EqualFold(device.ID, peerDeviceID) {
			c.JSON(http.StatusOK, models.TunnelPeerWrapKeyResponse{
				PeerCNSUserID: peerUserID,
				PeerDeviceID:  peerDeviceID,
				PublicKeyJWK:  device.PublicKeyJWK,
				KeyAlgorithm:  device.KeyAlgorithm,
				KeyVersion:    device.KeyVersion,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Peer device is not trusted", Code: "PEER_DEVICE_NOT_TRUSTED"})
}

func (h *TunnelHandler) generateUniqueTunnelCode(ctx context.Context) (string, error) {
	for i := 0; i < 10; i++ {
		code := generateVerificationCode(6)
		exists, err := h.db.TunnelCodeExists(ctx, code)
		if err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique tunnel code")
}

func (h *TunnelHandler) buildQRPayload(tunnel *models.Tunnel) string {
	payload, err := json.Marshal(gin.H{
		"p": "shareit-tunnel-v1",
		"s": h.cfg.BaseURL,
		"c": tunnel.Code,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func nullableDeviceID(value string) sql.NullString {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: trimmed, Valid: true}
}