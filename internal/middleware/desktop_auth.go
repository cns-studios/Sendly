package middleware

import (
	"net/http"
	"strings"

	"shareit/internal/config"
	"shareit/internal/models"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

const DesktopAPIKeyCtx = "desktop_api_key"

func DesktopAuthMiddleware(cfg *config.Config, db *storage.Postgres) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			if token := strings.TrimSpace(c.Query("token")); token != "" {
				authHeader = "Bearer " + token
			}
		}
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			user, err := ValidateCNSAccessToken(c.Request.Context(), cfg, token)
			if err == nil {
				c.Set(CNSUserKey, user)
				c.Next()
				return
			}
		}

		keyValue := c.GetHeader("X-API-KEY")
		if keyValue == "" {
			keyValue = strings.TrimSpace(c.Query("key"))
		}
		if keyValue == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "Missing authentication credentials",
				Code:  "MISSING_AUTH",
			})
			return
		}

		key, err := db.GetDesktopAPIKey(c.Request.Context(), keyValue)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: models.ErrAPIKeyNotFound.Message,
				Code:  models.ErrAPIKeyNotFound.Code,
			})
			return
		}

		c.Set(DesktopAPIKeyCtx, key)
		c.Next()
	}
}

func GetDesktopAPIKey(c *gin.Context) *models.DesktopAPIKey {
	val, _ := c.Get(DesktopAPIKeyCtx)
	key, _ := val.(*models.DesktopAPIKey)
	return key
}