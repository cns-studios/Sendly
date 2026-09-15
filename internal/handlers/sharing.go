package handlers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sendly/internal/middleware"
	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

func (h *RecentUploadsHandler) GetUserIdentityKey(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	targetID, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || targetID <= 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid user id", Code: "INVALID_USER_ID"})
		return
	}
	key, err := h.db.GetActiveUserIdentityKey(c.Request.Context(), targetID)
	if err != nil {
		if err == models.ErrIdentityKeyNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: models.ErrRecipientNotReady.Message, Code: models.ErrRecipientNotReady.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to look up recipient identity key", Code: "IDENTITY_KEY_LOOKUP_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"public_key_jwk": key.PublicKeyJWK,
		"key_version":    key.KeyVersion,
	})
}

func (h *RecentUploadsHandler) LookupUsers(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if len([]rune(query)) < 3 {
		c.JSON(http.StatusOK, gin.H{"items": []gin.H{}})
		return
	}
	if h.cfg.CNSAuthURL == "" {
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{Error: "User lookup unavailable", Code: "USER_LOOKUP_UNAVAILABLE"})
		return
	}
	token, err := c.Cookie("auth_token")
	if err != nil || token == "" {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
		strings.TrimSuffix(h.cfg.CNSAuthURL, "/")+"/api/users/lookup?q="+url.QueryEscape(query), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "User lookup failed", Code: "USER_LOOKUP_FAILED"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Error: "User lookup failed", Code: "USER_LOOKUP_FAILED"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Error: "User lookup failed", Code: "USER_LOOKUP_FAILED"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Error: "User lookup failed", Code: "USER_LOOKUP_FAILED"})
		return
	}
	var payload struct {
		Items []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Avatar   string `json:"avatar,omitempty"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Error: "User lookup failed", Code: "USER_LOOKUP_FAILED"})
		return
	}
	items := make([]gin.H, 0, len(payload.Items))
	for _, item := range payload.Items {
		items = append(items, gin.H{"user_id": item.ID, "username": item.Username, "avatar": item.Avatar})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *RecentUploadsHandler) RecentShareRecipients(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	ids, err := h.db.GetRecentShareRecipientIDs(c.Request.Context(), int64(user.ID), 8)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch recent recipients", Code: "RECENT_RECIPIENTS_FAILED"})
		return
	}
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		items = append(items, gin.H{"user_id": id, "username": strconv.FormatInt(id, 10)})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *RecentUploadsHandler) ShareFileToUser(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	fileID := strings.TrimSpace(c.Param("id"))
	var req models.ShareFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid share request", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}
	if req.RecipientUserID == int64(user.ID) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Recipient must be another user", Code: "INVALID_RECIPIENT"})
		return
	}
	if _, _, err := h.db.GetOwnedFileWithEnvelope(c.Request.Context(), int64(user.ID), fileID); err != nil {
		status := http.StatusForbidden
		if err == models.ErrFileNotFound || err == models.ErrFileExpired || err == models.ErrFileDeleted {
			if _, fileErr := h.db.GetFileByID(c.Request.Context(), fileID); fileErr != nil {
				status = http.StatusNotFound
			}
		}
		c.JSON(status, models.ErrorResponse{Error: "Only the file owner can share this file", Code: "FILE_OWNERSHIP_REQUIRED"})
		return
	}
	recipientKey, err := h.db.GetActiveUserIdentityKey(c.Request.Context(), req.RecipientUserID)
	if err != nil {
		if err == models.ErrIdentityKeyNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: models.ErrRecipientNotReady.Message, Code: models.ErrRecipientNotReady.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to look up recipient identity key", Code: "IDENTITY_KEY_LOOKUP_FAILED"})
		return
	}
	if req.RecipientKeyVersion != recipientKey.KeyVersion {
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: "Recipient identity key version is stale", Code: "RECIPIENT_KEY_VERSION_MISMATCH"})
		return
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedDEK)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped DEK", Code: "INVALID_WRAPPED_DEK"})
		return
	}
	var nonce []byte
	if req.DEKWrapNonce != "" {
		nonce, err = base64.StdEncoding.DecodeString(req.DEKWrapNonce)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid DEK wrap nonce", Code: "INVALID_DEK_WRAP_NONCE"})
			return
		}
	}
	now := time.Now()
	if err := h.db.CreateFileAccessKeyEnvelope(c.Request.Context(), &models.FileAccessKeyEnvelope{
		FileID: fileID, RecipientCNSUserID: req.RecipientUserID, WrappedDEK: wrapped,
		DEKWrapAlg: req.DEKWrapAlg, DEKWrapNonce: nonce, DEKWrapVersion: 1,
		RecipientKeyVersion: req.RecipientKeyVersion, AccessKind: "share",
		GrantedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create file share", Code: "FILE_SHARE_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"file_id": fileID, "recipient_user_id": req.RecipientUserID})
}

func (h *RecentUploadsHandler) SharedWithMe(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	page, perPage, ok := parsePagination(c)
	if !ok {
		return
	}
	items, total, err := h.db.GetSharedWithMeFiles(c.Request.Context(), int64(user.ID), page, perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch shared files", Code: "SHARED_FILES_FAILED"})
		return
	}
	for i := range items {
		items[i].ShareURL = h.cfg.BaseURL + "/shared/" + items[i].FileID
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	if totalPages > 0 && page > totalPages {
		page = totalPages
		items, total, err = h.db.GetSharedWithMeFiles(c.Request.Context(), int64(user.ID), page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch shared files", Code: "SHARED_FILES_FAILED"})
			return
		}
		totalPages = (total + perPage - 1) / perPage
	}
	c.JSON(http.StatusOK, models.SharedFilesResponse{Items: items, Page: page, PerPage: perPage, Total: total, TotalPages: totalPages})
}

func parsePagination(c *gin.Context) (int, int, bool) {
	page, perPage := 1, 10
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid page parameter", Code: "INVALID_PAGINATION"})
			return 0, 0, false
		}
		page = value
	}
	if raw := strings.TrimSpace(c.Query("per_page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid per_page parameter", Code: "INVALID_PAGINATION"})
			return 0, 0, false
		}
		if value > 50 {
			value = 50
		}
		perPage = value
	}
	return page, perPage, true
}
