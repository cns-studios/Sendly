package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"

	"shareit/internal/config"
	"shareit/internal/i18n"
	"shareit/internal/middleware"

	"github.com/gin-gonic/gin"
)

type PageHandler struct {
	cfg *config.Config
	tr  *i18n.Translator
}

func NewPageHandler(cfg *config.Config, tr *i18n.Translator) *PageHandler {
	return &PageHandler{
		cfg: cfg,
		tr:  tr,
	}
}

func (h *PageHandler) render(c *gin.Context, templateName string, data gin.H) {
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	if data == nil {
		data = gin.H{}
	}
	data["t"] = translations
	data["locale"] = locale
	if locale == "de" {
		data["otherLocale"] = "en"
		data["otherLocaleLabel"] = translations["lang_en"]
	} else {
		data["otherLocale"] = "de"
		data["otherLocaleLabel"] = translations["lang_de"]
	}
	c.HTML(http.StatusOK, templateName, data)
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
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "index.html", gin.H{
		"title":            translations["title_index"],
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
	user := middleware.GetCNSUser(c)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "tos.html", gin.H{
		"title":         translations["title_tos"],
		"baseURL":       h.cfg.BaseURL,
		"authenticated": authenticated,
		"authLoginURL":  authLoginURL,
		"username":      username,
	})
}

func (h *PageHandler) Privacy(c *gin.Context) {
	setCSRFTokenCookie(c)
	user := middleware.GetCNSUser(c)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "privacy.html", gin.H{
		"title":         translations["title_privacy"],
		"baseURL":       h.cfg.BaseURL,
		"authenticated": authenticated,
		"authLoginURL":  authLoginURL,
		"username":      username,
	})
}

func (h *PageHandler) LimitsPage(c *gin.Context) {
	setCSRFTokenCookie(c)
	user := middleware.GetCNSUser(c)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "limits.html", gin.H{
		"title":         translations["title_limits"],
		"baseURL":       h.cfg.BaseURL,
		"authenticated": authenticated,
		"authLoginURL":  authLoginURL,
		"username":      username,
	})
}

func (h *PageHandler) DataEncryption(c *gin.Context) {
	setCSRFTokenCookie(c)
	user := middleware.GetCNSUser(c)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "data-encryption.html", gin.H{
		"title":         translations["title_encryption"],
		"baseURL":       h.cfg.BaseURL,
		"authenticated": authenticated,
		"authLoginURL":  authLoginURL,
		"username":      username,
	})
}

func (h *PageHandler) HelpPage(c *gin.Context) {
	setCSRFTokenCookie(c)
	user := middleware.GetCNSUser(c)
	authenticated := user != nil
	username := ""
	if user != nil {
		username = user.Username
	}
	authLoginURL := ""
	if h.cfg.CNSAuthURL != "" {
		authLoginURL = "/auth/login"
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "help.html", gin.H{
		"title":         translations["title_help"],
		"baseURL":       h.cfg.BaseURL,
		"authenticated": authenticated,
		"authLoginURL":  authLoginURL,
		"username":      username,
	})
}

func (h *PageHandler) QuickShare(c *gin.Context) {
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
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "quickshare.html", gin.H{
		"title":            translations["title_quickshare"],
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

func (h *PageHandler) Link(c *gin.Context) {
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
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "link.html", gin.H{
		"title":            translations["title_link"],
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

func (h *PageHandler) SharedFile(c *gin.Context) {
	setCSRFTokenCookie(c)
	fileID := c.Param("id")
	user := middleware.GetCNSUser(c)
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
		"baseURL":    h.cfg.BaseURL,
		"fileID":     fileID,
		"tosVersion": h.cfg.TOSVersion,
	}
	configJSON, err := json.Marshal(configData)
	if err != nil {
		configJSON = []byte("{}")
	}
	locale := middleware.GetLocale(c)
	translations := h.tr.Get(locale)
	h.render(c, "shared.html", gin.H{
		"title":        translations["title_shared"],
		"baseURL":      h.cfg.BaseURL,
		"fileID":       fileID,
		"authenticated": authenticated,
		"username":     username,
		"authLoginURL": authLoginURL,
		"tosVersion":   h.cfg.TOSVersion,
		"configJSON":   template.JS(string(configJSON)),
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
