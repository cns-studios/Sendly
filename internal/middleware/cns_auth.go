package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shareit/internal/config"

	"github.com/gin-gonic/gin"
)

const CNSUserKey = "cns_user"

type CNSUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
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
		req.Header.Set("User-Agent", "ShareIt-Auth-Bridge/1.0")
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

func RefreshAccessToken(ctx context.Context, cfg *config.Config, refreshToken string) (*refreshTokenResult, error) {
	tokenURL := cfg.CNSAuthURL + "/v2/token/refresh"

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
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, rawBody)
	}

	var result refreshTokenResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	return &result, nil
}

func setAuthCookies(c *gin.Context, cfg *config.Config, token, refreshToken string, expiresIn int64) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	maxAge := int(expiresIn)
	if maxAge <= 0 {
		maxAge = 86400
	}
	expiresAt := time.Now().Unix() + int64(maxAge)

	c.SetCookie("auth_token", token, 3600*24*30, "/", "", isSecure, true)
	c.SetCookie("auth_expires_at", fmt.Sprintf("%d", expiresAt), 3600*24*30, "/", "", isSecure, true)
	if refreshToken != "" {
		c.SetCookie("refresh_token", refreshToken, 3600*24*30, "/", "", isSecure, true)
	}
}

func clearAuthTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	c.SetCookie("auth_token", "", -1, "/", "", isSecure, true)
	c.SetCookie("refresh_token", "", -1, "/", "", isSecure, true)
	c.SetCookie("auth_expires_at", "", -1, "/", "", isSecure, true)
}

func tryRefresh(c *gin.Context, cfg *config.Config) (newToken string, ok bool) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		return "", false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, refreshErr := RefreshAccessToken(ctx, cfg, refreshToken)
	if refreshErr != nil {
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

		// Proactive refresh: check if token is about to expire (within 60 seconds)
		if expiresAtStr, cookieErr := c.Cookie("auth_expires_at"); cookieErr == nil {
			if expiresAt, parseErr := strconv.ParseInt(expiresAtStr, 10, 64); parseErr == nil {
				if time.Now().Unix() >= expiresAt-60 {
					if newToken, ok := tryRefresh(c, cfg); ok {
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
				if newToken, ok := tryRefresh(c, cfg); ok {
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
