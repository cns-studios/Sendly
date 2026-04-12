package middleware

import (
	"context"
	"encoding/json"
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

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.CNSAuthURL+"/api/me", nil)
		if err != nil {
			c.Next()
			return
		}
		req.Header.Set("x-service-key", cfg.CNSAuthServiceKey)
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("Cookie", "auth_token="+authToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
				clearAuthTokenCookie(c, cfg)
			}
			c.Next()
			return
		}
		defer resp.Body.Close()

		var user CNSUser
		if err := json.NewDecoder(resp.Body).Decode(&user); err != nil || user.ID == 0 {
			clearAuthTokenCookie(c, cfg)
			c.Next()
			return
		}

		c.Set(CNSUserKey, &user)
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
