package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

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

	tunnel := &models.Tunnel{
		Code:               code,
		InitiatorCNSUserID: int64(user.ID),
		InitiatorDeviceID:   nullableString(req.DeviceID),
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

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

	joined, joinErr := h.db.JoinTunnel(c.Request.Context(), tunnel.ID, int64(user.ID), req.DeviceID)
	if joinErr != nil {
		if joinErr == models.ErrFileExpired {
			c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel expired", Code: "TUNNEL_EXPIRED"})
			return
		}
		if joinErr == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel not available for this account", Code: "TUNNEL_FORBIDDEN"})
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

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

	tunnel, err := h.db.ConfirmTunnel(c.Request.Context(), tunnelID, int64(user.ID), req.DeviceID)
	if err != nil {
		if err == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel does not belong to this account", Code: "TUNNEL_FORBIDDEN"})
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	if ok, err := h.db.TunnelBelongsToUser(c.Request.Context(), tunnelID, int64(user.ID)); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify tunnel access", Code: "TUNNEL_LOOKUP_FAILED"})
		return
	} else if !ok {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel does not belong to this account", Code: "TUNNEL_FORBIDDEN"})
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	if ok, err := h.db.TunnelBelongsToUser(c.Request.Context(), tunnelID, int64(user.ID)); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify tunnel access", Code: "TUNNEL_LOOKUP_FAILED"})
		return
	} else if !ok {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel does not belong to this account", Code: "TUNNEL_FORBIDDEN"})
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
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	if ok, err := h.db.TunnelBelongsToUser(c.Request.Context(), tunnelID, int64(user.ID)); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to verify tunnel access", Code: "TUNNEL_LOOKUP_FAILED"})
		return
	} else if !ok {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel does not belong to this account", Code: "TUNNEL_FORBIDDEN"})
		return
	}

	files, err := h.db.GetTunnelFiles(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load tunnel files", Code: "TUNNEL_FILES_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": files})
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
		"protocol":       "shareit-tunnel-v1",
		"server_url":     h.cfg.BaseURL,
		"tunnel_id":      tunnel.ID,
		"code":           tunnel.Code,
		"expires_at":     tunnel.ExpiresAt,
		"duration_minutes": tunnel.DurationMinutes,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}