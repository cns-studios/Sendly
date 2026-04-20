package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"shareit/internal/config"
	"shareit/internal/middleware"
	"shareit/internal/models"
	"shareit/internal/services"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

type UploadHandler struct {
	cfg           *config.Config
	db            *storage.Postgres
	redis         *storage.Redis
	fs            *storage.Filesystem
	uploadService *services.Upload
}

func NewUploadHandler(
	cfg *config.Config,
	db *storage.Postgres,
	redis *storage.Redis,
	fs *storage.Filesystem,
	uploadService *services.Upload,
) *UploadHandler {
	return &UploadHandler{
		cfg:           cfg,
		db:            db,
		redis:         redis,
		fs:            fs,
		uploadService: uploadService,
	}
}

func (h *UploadHandler) Init(c *gin.Context) {
	var req models.UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
		return
	}

	tier := middleware.GetTier(h.cfg, middleware.GetCNSUser(c))
	if req.FileSize > tier.MaxFileSize {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrFileTooLarge.Message,
			Code:  models.ErrFileTooLarge.Code,
		})
		return
	}

	clientIP := middleware.GetClientIP(c)

	resp, err := h.uploadService.InitUpload(c.Request.Context(), &req, clientIP)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to initialize upload",
			Code:  "UPLOAD_INIT_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *UploadHandler) AssemblyStatus(c *gin.Context) {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing session_id",
			Code:  "MISSING_SESSION_ID",
		})
		return
	}

	status, err := h.uploadService.GetAssemblyStatus(c.Request.Context(), sessionID)
	if err != nil {
		if err == models.ErrSessionNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{
				Error: "Assembly status not found",
				Code:  "SESSION_NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to get assembly status",
			Code:  "STATUS_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"status":     status,
	})
}

func (h *UploadHandler) Finalize(c *gin.Context) {
	var req models.UploadFinalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
		return
	}

	user := middleware.GetCNSUser(c)
	tier := middleware.GetTier(h.cfg, user)
	var opts *services.FinalizeUploadOptions
	if req.TunnelID != "" {
		opts = &services.FinalizeUploadOptions{}
		if user != nil {
			uid := int64(user.ID)
			uname := user.Username
			opts = &services.FinalizeUploadOptions{
				OwnerCNSUserID:   &uid,
				OwnerCNSUserName: &uname,
			}

			if req.WrappedDEKB64 == "" {
				c.JSON(http.StatusBadRequest, models.ErrorResponse{
					Error: "Trusted device approval is required before authenticated uploads can be finalized",
					Code:  "WRAPPED_DEK_REQUIRED",
				})
				return
			}
		}

		if req.WrappedDEKB64 != "" {
			wrappedDEK, decodeErr := base64.StdEncoding.DecodeString(req.WrappedDEKB64)
			if decodeErr != nil {
				c.JSON(http.StatusBadRequest, models.ErrorResponse{
					Error:   "Invalid wrapped DEK",
					Code:    "INVALID_WRAPPED_DEK",
					Details: decodeErr.Error(),
				})
				return
			}
			opts.WrappedDEK = wrappedDEK
			opts.DEKWrapAlg = req.DEKWrapAlg
			opts.DEKWrapVersion = req.DEKWrapVersion

			if req.DEKWrapNonceB64 != "" {
				nonce, nonceErr := base64.StdEncoding.DecodeString(req.DEKWrapNonceB64)
				if nonceErr != nil {
					c.JSON(http.StatusBadRequest, models.ErrorResponse{
						Error:   "Invalid DEK wrap nonce",
						Code:    "INVALID_DEK_WRAP_NONCE",
						Details: nonceErr.Error(),
					})
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
		if user != nil {
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
		}
		opts.TunnelID = req.TunnelID
		opts.TunnelExpiresAt = tunnel.ExpiresAt
	} else {
		if !tier.IsDurationAllowed(req.Duration) {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error: "Duration not available for your account tier",
				Code:  "DURATION_NOT_ALLOWED",
			})
			return
		}
	}

	if req.TunnelID == "" && req.Duration == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Duration is required for non-tunnel uploads", Code: "DURATION_REQUIRED"})
		return
	}

	resp, err := h.uploadService.FinalizeUploadWithOptions(c.Request.Context(), req.SessionID, req.Duration, opts)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusBadRequest
			if appErr == models.ErrSessionNotFound || appErr == models.ErrSessionExpired {
				status = http.StatusNotFound
			}
			c.JSON(status, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Failed to finalize upload",
			Code:    "FINALIZE_FAILED",
			Details: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *UploadHandler) Chunk(c *gin.Context) {

	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Failed to parse multipart form",
			Code:    "PARSE_ERROR",
			Details: err.Error(),
		})
		return
	}

	sessionID := c.PostForm("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing session_id",
			Code:  "MISSING_SESSION_ID",
		})
		return
	}

	chunkIndexStr := c.PostForm("chunk_index")
	if chunkIndexStr == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing chunk_index",
			Code:  "MISSING_CHUNK_INDEX",
		})
		return
	}

	var chunkIndex int
	if _, err := fmt.Sscanf(chunkIndexStr, "%d", &chunkIndex); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid chunk_index",
			Code:    "INVALID_CHUNK_INDEX",
			Details: err.Error(),
		})
		return
	}

	file, _, err := c.Request.FormFile("chunk")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Missing chunk file",
			Code:    "MISSING_CHUNK",
			Details: err.Error(),
		})
		return
	}
	defer file.Close()

	err = h.uploadService.UploadChunk(c.Request.Context(), sessionID, chunkIndex, file)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusBadRequest
			if appErr == models.ErrSessionNotFound || appErr == models.ErrSessionExpired {
				status = http.StatusNotFound
			}
			c.JSON(status, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to upload chunk",
			Code:  "CHUNK_UPLOAD_FAILED",
		})
		return
	}

	uploaded, total, err := h.uploadService.GetUploadProgress(c.Request.Context(), sessionID)
	if err != nil {

		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"chunk_index": chunkIndex,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"chunk_index":     chunkIndex,
		"uploaded_chunks": uploaded,
		"total_chunks":    total,
	})
}

func (h *UploadHandler) Complete(c *gin.Context) {
	var req models.UploadCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
		return
	}

	if !req.Confirmed {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Upload must be confirmed",
			Code:  "NOT_CONFIRMED",
		})
		return
	}

	resp, err := h.uploadService.CompleteUpload(c.Request.Context(), req.SessionID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusBadRequest
			if appErr == models.ErrSessionNotFound || appErr == models.ErrSessionExpired {
				status = http.StatusNotFound
			}
			c.JSON(status, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Failed to complete upload",
			Code:    "COMPLETE_FAILED",
			Details: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *UploadHandler) Cancel(c *gin.Context) {
	var req models.UploadCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
		return
	}

	err := h.uploadService.CancelUpload(c.Request.Context(), req.SessionID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to cancel upload",
			Code:  "CANCEL_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Upload cancelled",
	})
}

func (h *UploadHandler) Progress(c *gin.Context) {
	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing session_id",
			Code:  "MISSING_SESSION_ID",
		})
		return
	}

	uploaded, total, err := h.uploadService.GetUploadProgress(c.Request.Context(), sessionID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusBadRequest
			if appErr == models.ErrSessionNotFound {
				status = http.StatusNotFound
			}
			c.JSON(status, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to get progress",
			Code:  "PROGRESS_FAILED",
		})
		return
	}

	percentage := 0.0
	if total > 0 {
		percentage = float64(uploaded) / float64(total) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"uploaded_chunks": uploaded,
		"total_chunks":    total,
		"percentage":      percentage,
	})
}
