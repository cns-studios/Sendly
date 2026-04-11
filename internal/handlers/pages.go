package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"

	"shareit/internal/config"
	"shareit/internal/middleware"

	"github.com/gin-gonic/gin"
)

type PageHandler struct {
	cfg *config.Config
}

func NewPageHandler(cfg *config.Config) *PageHandler {
	return &PageHandler{
		cfg: cfg,
	}
}

func setCSRFTokenCookie(c *gin.Context) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return
	}
	token := hex.EncodeToString(tokenBytes)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("csrf_token", token, 86400, "/", "", false, false)
}

func (h *PageHandler) Index(c *gin.Context) {
	setCSRFTokenCookie(c)
	user := middleware.GetCNSUser(c)
	tier := middleware.GetTier(h.cfg, user)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	configData := map[string]interface{}{
		"baseURL":          h.cfg.BaseURL,
		"maxFileSize":      tier.MaxFileSize,
		"authMaxFileSize":  h.cfg.AuthMaxFileSize,
		"authenticated":    authenticated,
		"cnsUserId":        userIDOrZero(user),
		"cnsUsername":      username,
		"allowedDurations": tier.AllowedDurations,
		"tosVersion":       h.cfg.TOSVersion,
	}
	configJSON, err := json.Marshal(configData)
	if err != nil {
		configJSON = []byte("{}")
	}
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title":            "ShareIt - End-to-End Encrypted File Sharing",
		"baseURL":          h.cfg.BaseURL,
		"maxFileSize":      tier.MaxFileSize,
		"authMaxFileSize":  h.cfg.AuthMaxFileSize,
		"authenticated":    authenticated,
		"allowedDurations": tier.AllowedDurations,
		"tosVersion":       h.cfg.TOSVersion,
		"authLoginURL":     authLoginURL,
		"username":         username,
		"configJSON":       template.JS(string(configJSON)),
	})
}

func (h *PageHandler) ToS(c *gin.Context) {
	setCSRFTokenCookie(c)
	c.HTML(http.StatusOK, "tos.html", gin.H{
		"title":   "Terms of Service - ShareIt",
		"baseURL": h.cfg.BaseURL,
	})
}

func (h *PageHandler) Privacy(c *gin.Context) {
	setCSRFTokenCookie(c)
	c.HTML(http.StatusOK, "privacy.html", gin.H{
		"title":   "Privacy Policy - ShareIt",
		"baseURL": h.cfg.BaseURL,
	})
}

func (h *PageHandler) SharedLookup(c *gin.Context) {
	setCSRFTokenCookie(c)
	c.HTML(http.StatusOK, "shared_lookup.html", gin.H{
		"title":   "Retrieve File - ShareIt",
		"baseURL": h.cfg.BaseURL,
	})
}

func (h *PageHandler) SharedFile(c *gin.Context) {
	setCSRFTokenCookie(c)
	fileID := c.Param("id")

	c.HTML(http.StatusOK, "shared.html", gin.H{
		"title":   "Download File - ShareIt",
		"baseURL": h.cfg.BaseURL,
		"fileID":  fileID,
	})
}

func (h *PageHandler) Limits(c *gin.Context) {
	tier := middleware.GetTier(h.cfg, middleware.GetCNSUser(c))
	c.JSON(http.StatusOK, gin.H{
		"max_file_size":     tier.MaxFileSize,
		"allowed_durations": tier.AllowedDurations,
		"authenticated":     middleware.GetCNSUser(c) != nil,
	})
}

func userIDOrZero(user *middleware.CNSUser) int {
	if user == nil {
		return 0
	}
	return user.ID
}
