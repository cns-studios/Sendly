package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sync"

	"shareit/internal/config"
	"shareit/internal/middleware"
	"shareit/internal/models"
	"shareit/internal/services"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}


type desktopHub struct {
	mu      sync.Mutex
	conns   map[string][]*websocket.Conn
}

func newDesktopHub() *desktopHub {
	return &desktopHub{conns: make(map[string][]*websocket.Conn)}
}

func (h *desktopHub) add(apiKeyID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[apiKeyID] = append(h.conns[apiKeyID], conn)
}

func (h *desktopHub) notify(apiKeyID, fileName string, meta *models.DesktopFileMetadata) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.conns[apiKeyID]
	msg := map[string]interface{}{
		"type": "new_file",
		"file": meta,
	}

	alive := conns[:0]
	for _, conn := range conns {
		if err := conn.WriteJSON(msg); err != nil {
			conn.Close()
		} else {
			alive = append(alive, conn)
		}
	}
	h.conns[apiKeyID] = alive
}


type DesktopHandler struct {
	cfg           *config.Config
	db            *storage.Postgres
	fs            *storage.Filesystem
	uploadService *services.Upload
	hub           *desktopHub
}

func NewDesktopHandler(
	cfg *config.Config,
	db *storage.Postgres,
	fs *storage.Filesystem,
	uploadService *services.Upload,
) *DesktopHandler {
	return &DesktopHandler{
		cfg:           cfg,
		db:            db,
		fs:            fs,
		uploadService: uploadService,
		hub:           newDesktopHub(),
	}
}



func (h *DesktopHandler) VerifyKey(c *gin.Context) {
	keyValue := c.Query("key")
	if keyValue == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing key parameter",
			Code:  "MISSING_KEY",
		})
		return
	}

	key, err := h.db.GetDesktopAPIKey(c.Request.Context(), keyValue)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{
			Error: models.ErrAPIKeyNotFound.Message,
			Code:  models.ErrAPIKeyNotFound.Code,
		})
		return
	}

	c.JSON(http.StatusOK, models.DesktopVerifyResponse{
		Status: "valid",
		Owner:  key.OwnerName,
	})
}

func (h *DesktopHandler) OAuthConfig(c *gin.Context) {
	clientID := h.cfg.DesktopOAuthClientID()
	if h.cfg.CNSAuthURL == "" || clientID == "" {
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
			Error: "OAuth is not configured",
			Code:  "OAUTH_NOT_CONFIGURED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"auth_url":  h.cfg.CNSAuthURL,
		"client_id": clientID,
	})
}

func (h *DesktopHandler) OAuthVerify(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	const bearerPrefix = "Bearer "
	if len(authHeader) <= len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Missing bearer token", Code: "MISSING_BEARER_TOKEN"})
		return
	}

	token := authHeader[len(bearerPrefix):]
	user, err := middleware.ValidateCNSAccessToken(c.Request.Context(), h.cfg, token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Invalid bearer token", Code: "INVALID_BEARER_TOKEN"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "valid",
		"owner":    user.Username,
		"cns_user": user,
	})
}


func (h *DesktopHandler) UploadInit(c *gin.Context) {
	key := middleware.GetDesktopAPIKey(c)

	var req models.UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error(),
		})
		return
	}

	if req.FileSize > h.cfg.MaxFileSize {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: models.ErrFileTooLarge.Message, Code: models.ErrFileTooLarge.Code,
		})
		return
	}

	clientIP := middleware.GetClientIP(c)
	resp, err := h.uploadService.InitUpload(c.Request.Context(), &req, clientIP)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to initialize upload", Code: "UPLOAD_INIT_FAILED"})
		return
	}

	
	respBody := gin.H{
		"session_id":   resp.SessionID,
		"file_id":      resp.FileID,
		"chunk_size":   resp.ChunkSize,
		"total_chunks": resp.TotalChunks,
	}
	if key != nil {
		respBody["api_key_id"] = key.ID
	}

	c.JSON(http.StatusOK, respBody)
}


func (h *DesktopHandler) UploadChunk(c *gin.Context) {
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Failed to parse form", Code: "PARSE_ERROR"})
		return
	}

	sessionID := c.PostForm("session_id")
	chunkIndexStr := c.PostForm("chunk_index")
	if sessionID == "" || chunkIndexStr == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing fields", Code: "MISSING_FIELDS"})
		return
	}

	var chunkIndex int
	fmt.Sscanf(chunkIndexStr, "%d", &chunkIndex)

	file, _, err := c.Request.FormFile("chunk")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing chunk file", Code: "MISSING_CHUNK"})
		return
	}
	defer file.Close()

	if err := h.uploadService.UploadChunk(c.Request.Context(), sessionID, chunkIndex, file); err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Chunk upload failed", Code: "CHUNK_UPLOAD_FAILED"})
		return
	}

	uploaded, total, _ := h.uploadService.GetUploadProgress(c.Request.Context(), sessionID)
	c.JSON(http.StatusOK, gin.H{"success": true, "chunk_index": chunkIndex, "uploaded_chunks": uploaded, "total_chunks": total})
}


func (h *DesktopHandler) UploadComplete(c *gin.Context) {
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


func (h *DesktopHandler) UploadFinalize(c *gin.Context) {
	key := middleware.GetDesktopAPIKey(c)
	user := middleware.GetCNSUser(c)

	var req models.DesktopFinalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request", Code: "INVALID_REQUEST"})
		return
	}

	tier := middleware.GetTier(h.cfg, user)
	if !tier.IsDurationAllowed(req.Duration) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Duration not available for your account tier", Code: "DURATION_NOT_ALLOWED"})
		return
	}

	var opts *services.FinalizeUploadOptions
	if user != nil {
		uid := int64(user.ID)
		uname := user.Username
		opts = &services.FinalizeUploadOptions{
			OwnerCNSUserID:   &uid,
			OwnerCNSUserName: &uname,
		}

		if req.WrappedDEKB64 == "" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Trusted device approval is required before authenticated uploads can be finalized", Code: "WRAPPED_DEK_REQUIRED"})
			return
		}

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

	baseResp, err := h.uploadService.FinalizeUploadWithOptions(c.Request.Context(), req.SessionID, req.Duration, opts)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Finalize failed", Code: "FINALIZE_FAILED"})
		return
	}

	
	if key != nil {
		if err := h.db.AssociateFileWithKey(c.Request.Context(), baseResp.FileID, key.ID); err != nil {
		
			fmt.Printf("Warning: failed to associate file %s with key %s: %v\n", baseResp.FileID, key.ID, err)
		}
	}

	channelID := ""
	if key != nil {
		channelID = "key:" + key.ID
	} else if user != nil {
		channelID = fmt.Sprintf("user:%d", user.ID)
	}

	var files []models.DesktopFileMetadata
	if key != nil {
		files, _ = h.db.ListFilesByAPIKey(context.Background(), key.ID, 50, 0)
	} else if user != nil {
		items, _, _ := h.db.GetOwnedRecentFiles(context.Background(), int64(user.ID), "", 1, 50)
		files = make([]models.DesktopFileMetadata, 0, len(items))
		for _, item := range items {
			files = append(files, models.DesktopFileMetadata{
				ID:         item.FileID,
				FileName:   item.Filename,
				FileSize:   item.SizeBytes,
				ExpiresAt:  item.ExpiresAt,
				UploadedAt: item.CreatedAt,
			})
		}
	}

	var meta *models.DesktopFileMetadata
	for i := range files {
		if files[i].ID == baseResp.FileID {
			meta = &files[i]
			break
		}
	}

	if meta != nil {
		if channelID != "" {
			h.hub.notify(channelID, meta.FileName, meta)
		}
		c.JSON(http.StatusOK, models.DesktopFinalizeResponse{
			FileID:      baseResp.FileID,
			NumericCode: baseResp.NumericCode,
			FileName:    meta.FileName,
			FileSize:    meta.FileSize,
			ExpiresAt:   meta.ExpiresAt,
			ShareURL:    baseResp.ShareURL,
		})
		return
	}

	c.JSON(http.StatusOK, models.DesktopFinalizeResponse{
		FileID:      baseResp.FileID,
		NumericCode: baseResp.NumericCode,
		ShareURL:    baseResp.ShareURL,
	})
}


func (h *DesktopHandler) UploadStatus(c *gin.Context) {
	sessionID := c.Param("session_id")
	status, err := h.uploadService.GetAssemblyStatus(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Session not found", Code: "SESSION_NOT_FOUND"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "status": status})
}


func (h *DesktopHandler) ListFiles(c *gin.Context) {
	key := middleware.GetDesktopAPIKey(c)
	user := middleware.GetCNSUser(c)

	if key == nil && user != nil {
		items, _, err := h.db.GetOwnedRecentFiles(c.Request.Context(), int64(user.ID), "", 1, 50)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list files", Code: "LIST_FAILED"})
			return
		}
		files := make([]models.DesktopFileMetadata, 0, len(items))
		for _, item := range items {
			files = append(files, models.DesktopFileMetadata{
				ID:         item.FileID,
				FileName:   item.Filename,
				FileSize:   item.SizeBytes,
				ExpiresAt:  item.ExpiresAt,
				UploadedAt: item.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, files)
		return
	}
	if key == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	files, err := h.db.ListFilesByAPIKey(c.Request.Context(), key.ID, 50, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list files", Code: "LIST_FAILED"})
		return
	}
	if files == nil {
		files = []models.DesktopFileMetadata{}
	}
	c.JSON(http.StatusOK, files)
}


func (h *DesktopHandler) GetFile(c *gin.Context) {
	key := middleware.GetDesktopAPIKey(c)
	user := middleware.GetCNSUser(c)
	fileID := c.Param("id")

	if key == nil && user != nil {
		file, _, err := h.db.GetOwnedFileWithEnvelope(c.Request.Context(), int64(user.ID), fileID)
		if err != nil {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Unable to access this file", Code: "ACCESS_DENIED"})
			return
		}
		c.JSON(http.StatusOK, file.ToMetadata())
		return
	}
	if key == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	owned, err := h.db.FileOwnedByKey(c.Request.Context(), fileID, key.ID)
	if err != nil || !owned {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: models.ErrFileNotOwned.Message, Code: models.ErrFileNotOwned.Code})
		return
	}

	file, err := h.db.GetFileByID(c.Request.Context(), fileID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get file", Code: "GET_FAILED"})
		return
	}
	c.JSON(http.StatusOK, file.ToMetadata())
}


func (h *DesktopHandler) DownloadFile(c *gin.Context) {
	key := middleware.GetDesktopAPIKey(c)
	user := middleware.GetCNSUser(c)
	fileID := c.Param("id")

	if key == nil && user != nil {
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
		io.Copy(c.Writer, reader)
		return
	}
	if key == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	owned, err := h.db.FileOwnedByKey(c.Request.Context(), fileID, key.ID)
	if err != nil || !owned {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: models.ErrFileNotOwned.Message, Code: models.ErrFileNotOwned.Code})
		return
	}

	file, err := h.db.GetFileByID(c.Request.Context(), fileID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get file", Code: "GET_FAILED"})
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
	io.Copy(c.Writer, reader)
}



func (h *DesktopHandler) WebSocket(c *gin.Context) {
	tokenQuery := c.Query("token")
	if tokenQuery != "" {
		user, err := middleware.ValidateCNSAccessToken(c.Request.Context(), h.cfg, tokenQuery)
		if err == nil {
			conn, upgradeErr := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
			if upgradeErr != nil {
				return
			}

			h.hub.add(fmt.Sprintf("user:%d", user.ID), conn)
			go func() {
				defer conn.Close()
				for {
					if _, _, readErr := conn.ReadMessage(); readErr != nil {
						break
					}
				}
			}()
			return
		}
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		const bearerPrefix = "Bearer "
		if len(authHeader) > len(bearerPrefix) && authHeader[:len(bearerPrefix)] == bearerPrefix {
			token := authHeader[len(bearerPrefix):]
			user, err := middleware.ValidateCNSAccessToken(c.Request.Context(), h.cfg, token)
			if err == nil {
				conn, upgradeErr := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
				if upgradeErr != nil {
					return
				}

				h.hub.add(fmt.Sprintf("user:%d", user.ID), conn)
				go func() {
					defer conn.Close()
					for {
						if _, _, readErr := conn.ReadMessage(); readErr != nil {
							break
						}
					}
				}()
				return
			}
		}
	}

	keyValue := c.Query("key")
	key, err := h.db.GetDesktopAPIKey(c.Request.Context(), keyValue)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: models.ErrAPIKeyNotFound.Message, Code: models.ErrAPIKeyNotFound.Code})
		return
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	h.hub.add("key:"+key.ID, conn)

	
	go func() {
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}