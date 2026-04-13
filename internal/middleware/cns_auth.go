package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, cfg.CNSAuthURL+"/api/account/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

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

func clearAuthTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	c.SetCookie("auth_token", "", -1, "/", "", isSecure, true)
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

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		user, err := ValidateCNSAccessToken(ctx, cfg, authToken)
		if err != nil {
			if strings.Contains(err.Error(), "status 401") || strings.Contains(err.Error(), "status 403") {
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
