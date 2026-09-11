package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

const CNSUserKey = "cns_user"

type CNSUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar,omitempty"`
}

func (u *CNSUser) UnmarshalJSON(data []byte) error {
	type cnsUserAlias CNSUser
	var payload struct {
		cnsUserAlias
		AvatarURL      string `json:"avatar_url"`
		AvatarURLCamel string `json:"avatarUrl"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*u = CNSUser(payload.cnsUserAlias)
	if u.Avatar == "" {
		u.Avatar = payload.AvatarURL
	}
	if u.Avatar == "" {
		u.Avatar = payload.AvatarURLCamel
	}
	if u.Avatar == "" {
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err == nil {
			u.Avatar = findAvatarURL(raw)
		}
	}
	return nil
}

func findAvatarURL(value interface{}) string {
	switch value := value.(type) {
	case map[string]interface{}:
		for key, nested := range value {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "avatar" || normalized == "avatarurl" || normalized == "profileimage" || normalized == "profilepicture" || normalized == "picture" {
				if candidate, ok := nested.(string); ok && strings.HasPrefix(candidate, "http") {
					return candidate
				}
			}
			if candidate := findAvatarURL(nested); candidate != "" {
				return candidate
			}
		}
	case []interface{}:
		for _, nested := range value {
			if candidate := findAvatarURL(nested); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func ValidateCNSAccessToken(ctx context.Context, cfg *config.Config, token string) (*CNSUser, error) {
	if cfg.CNSAuthURL == "" {
		return nil, fmt.Errorf("cns auth is not configured")
	}
	if token == "" {
		return nil, fmt.Errorf("missing access token")
	}

	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	validateAtPath := func(path string, serviceKey string) (*CNSUser, error) {
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, cfg.CNSAuthURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		if serviceKey != "" {
			req.Header.Set("x-service-key", serviceKey)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("token validation failed with status %d", resp.StatusCode)
		}

		var user CNSUser
		if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
			return nil, err
		}
		if user.ID == 0 {
			return nil, fmt.Errorf("token resolved to empty user")
		}

		return &user, nil
	}

	if cfg.CNSAuthServiceKey != "" {
		if user, err := validateAtPath("/api/me", cfg.CNSAuthServiceKey); err == nil {
			return user, nil
		}
	}

	if user, err := validateAtPath("/api/account/me", ""); err == nil {
		return user, nil
	}

	return nil, fmt.Errorf("token validation failed")
}

type refreshTokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// tokenRefreshError carries the upstream status so callers can distinguish a
// dead token (401/403) from a transient upstream failure (5xx, network).
type tokenRefreshError struct {
	status int
	msg    string
}

const refreshResultCacheTTL = 10 * time.Second

var refreshCoordinator = struct {
	sync.Mutex
	results map[[32]byte]cachedRefreshResult
}{results: make(map[[32]byte]cachedRefreshResult)}

type cachedRefreshResult struct {
	result    *refreshTokenResult
	expiresAt time.Time
}

func (e *tokenRefreshError) Error() string { return e.msg }

func (e *tokenRefreshError) deadToken() bool {
	return e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
}

// IsDeadTokenError reports whether the given refresh error means the token
// itself was rejected by the auth server (401/403) rather than an upstream
// outage or transient network failure.
func IsDeadTokenError(err error) bool {
	var tre *tokenRefreshError
	return errors.As(err, &tre) && tre.deadToken()
}

func RefreshAccessToken(ctx context.Context, cfg *config.Config, refreshToken string) (*refreshTokenResult, error) {
	tokenKey := sha256.Sum256([]byte(refreshToken))
	refreshCoordinator.Lock()
	defer refreshCoordinator.Unlock()

	now := time.Now()
	for key, cached := range refreshCoordinator.results {
		if now.After(cached.expiresAt) {
			delete(refreshCoordinator.results, key)
		}
	}
	if cached, ok := refreshCoordinator.results[tokenKey]; ok {
		return cached.result, nil
	}

	tokenURL := cfg.CNSAuthURL + "/api/auth/token/refresh"

	jsonPayload := map[string]string{
		"refresh_token": refreshToken,
		"client_id":     cfg.CNSAuthClientID,
	}
	body, err := json.Marshal(jsonPayload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	rawBody := strings.TrimSpace(string(raw))

	if resp.StatusCode != http.StatusOK {
		return nil, &tokenRefreshError{status: resp.StatusCode, msg: fmt.Sprintf("token refresh failed with status %d: %s", resp.StatusCode, rawBody)}
	}

	var result refreshTokenResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	refreshCoordinator.results[tokenKey] = cachedRefreshResult{
		result:    &result,
		expiresAt: time.Now().Add(refreshResultCacheTTL),
	}
	return &result, nil
}

func authCookieDomain(cfg *config.Config) string {
	if strings.Contains(cfg.BaseURL, "localhost") {
		return ""
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return "." + parsed.Hostname()
}

func setCookieForAuthScope(c *gin.Context, name, value string, maxAge int, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	domain := authCookieDomain(cfg)
	c.SetCookie(name, value, maxAge, "/", domain, isSecure, true)
	if domain != "" {
		// Remove older host-only cookies created before domain scoping was consistent.
		c.SetCookie(name, "", -1, "/", "", isSecure, true)
	}
}

func setAuthCookies(c *gin.Context, cfg *config.Config, token, refreshToken string, expiresIn int64) {
	maxAge := int(expiresIn)
	if maxAge <= 0 {
		maxAge = 86400
	}
	expiresAt := time.Now().Unix() + int64(maxAge)

	c.SetSameSite(http.SameSiteLaxMode)
	setCookieForAuthScope(c, "auth_token", token, 3600*24*30, cfg)
	setCookieForAuthScope(c, "auth_expires_at", fmt.Sprintf("%d", expiresAt), 3600*24*30, cfg)
	if refreshToken != "" {
		setCookieForAuthScope(c, "refresh_token", refreshToken, 3600*24*30, cfg)
	}
}

func clearAuthTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	for _, domain := range []string{"", authCookieDomain(cfg)} {
		c.SetCookie("auth_token", "", -1, "/", domain, isSecure, true)
		c.SetCookie("refresh_token", "", -1, "/", domain, isSecure, true)
		c.SetCookie("auth_expires_at", "", -1, "/", domain, isSecure, true)
	}
}

func clearRefreshTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	for _, domain := range []string{"", authCookieDomain(cfg)} {
		c.SetCookie("refresh_token", "", -1, "/", domain, isSecure, true)
	}
}

// ClearRefreshTokenCookie expires the refresh_token cookie on the response.
func ClearRefreshTokenCookie(c *gin.Context, cfg *config.Config) {
	clearRefreshTokenCookie(c, cfg)
}

// refreshAccessToken attempts to refresh the access token using the refresh token cookie.
// Returns the new access token on success, empty string on failure.
func refreshAccessToken(c *gin.Context, cfg *config.Config) (string, bool) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		return "", false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, refreshErr := RefreshAccessToken(ctx, cfg, refreshToken)
	if refreshErr != nil {
		// Only treat the token as dead when the auth server says so (401/403).
		// Transient failures (5xx, network) must not log the user out; the
		// next request simply retries. A dead token is cleared immediately so
		// subsequent requests don't keep hammering the refresh endpoint.
		if IsDeadTokenError(refreshErr) {
			clearRefreshTokenCookie(c, cfg)
		}
		return "", false
	}

	setAuthCookies(c, cfg, result.AccessToken, result.RefreshToken, result.ExpiresIn)
	return result.AccessToken, true
}

func CNSAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.CNSAuthURL == "" || cfg.CNSAuthServiceKey == "" {
			c.Next()
			return
		}

		authToken, err := c.Cookie("auth_token")
		if err != nil || authToken == "" {
			c.Next()
			return
		}

		if expiresAtStr, cookieErr := c.Cookie("auth_expires_at"); cookieErr == nil {
			if expiresAt, parseErr := strconv.ParseInt(expiresAtStr, 10, 64); parseErr == nil {
				if time.Now().Unix() >= expiresAt-60 {
					if newToken, ok := refreshAccessToken(c, cfg); ok {
						authToken = newToken
					}
				}
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		user, err := ValidateCNSAccessToken(ctx, cfg, authToken)
		if err != nil {
			if strings.Contains(err.Error(), "status 401") || strings.Contains(err.Error(), "status 403") {
				if newToken, ok := refreshAccessToken(c, cfg); ok {
					newCtx, newCancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
					defer newCancel()
					user, err = ValidateCNSAccessToken(newCtx, cfg, newToken)
					newCancel()
					if err == nil {
						c.Set(CNSUserKey, user)
						c.Next()
						return
					}
				}
				clearAuthTokenCookie(c, cfg)
			}
			c.Next()
			return
		}

		if user.Avatar == "" {
			if avatar, cookieErr := c.Cookie("auth_avatar"); cookieErr == nil {
				user.Avatar = avatar
			}
		}
		c.Set(CNSUserKey, user)
		c.Next()
	}
}

func GetCNSUser(c *gin.Context) *CNSUser {
	val, exists := c.Get(CNSUserKey)
	if !exists {
		return nil
	}
	user, _ := val.(*CNSUser)
	return user
}
