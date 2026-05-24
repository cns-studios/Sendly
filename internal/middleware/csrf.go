package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		mutating := map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}
		if mutating[c.Request.Method] && strings.HasPrefix(c.Request.URL.Path, "/api/") {
			cookieToken, err := c.Cookie("csrf_token")
			headerToken := c.GetHeader("X-CSRF-Token")
			if err != nil || cookieToken == "" || headerToken == "" || subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "invalid CSRF token",
					"code":  "CSRF_FORBIDDEN",
				})
				return
			}
		}

		c.Next()
	}
}
