package middleware

import (
	"net/http"
	"strings"

	"shareit/internal/config"
	"shareit/internal/models"

	"github.com/gin-gonic/gin"
)

func AndroidAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if authHeader == "" {
			if token := strings.TrimSpace(c.Query("token")); token != "" {
				authHeader = "Bearer " + token
			}
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "Missing bearer token",
				Code:  "MISSING_BEARER_TOKEN",
			})
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		user, err := ValidateCNSAccessToken(c.Request.Context(), cfg, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error:   "Invalid bearer token",
				Code:    "INVALID_BEARER_TOKEN",
				Details: err.Error(),
			})
			return
		}

		c.Set(CNSUserKey, user)
		c.Next()
	}
}
