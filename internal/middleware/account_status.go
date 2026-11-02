package middleware

import (
	"net/http"
	"strings"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

func UserStatus(user *CNSUser) accountstatus.Status {
	if user == nil {
		return accountstatus.Active
	}
	return user.AccountStatus.Effective(time.Now())
}

func UserAllows(user *CNSUser, capability accountstatus.Capability) bool {
	return UserStatus(user).Allows(capability)
}

func AbortRestricted(c *gin.Context, capability accountstatus.Capability) {
	c.AbortWithStatusJSON(http.StatusForbidden, models.ErrorResponse{
		Error:   models.ErrAccountRestricted.Message,
		Code:    models.ErrAccountRestricted.Code,
		Details: string(capability),
	})
}

func RequireCapability(capability accountstatus.Capability) gin.HandlerFunc {
	return func(c *gin.Context) {
		if user := GetCNSUser(c); user != nil && !UserAllows(user, capability) {
			AbortRestricted(c, capability)
			return
		}
		c.Next()
	}
}

var suspendedAllowedPrefixes = []string{
	"/static/",
	"/auth/",
	"/webhooks/",
	"/.well-known/",
}

var suspendedAllowedPaths = map[string]bool{
	"/health":       true,
	"/livez":        true,
	"/readyz":       true,
	"/robots.txt":   true,
	"/sitemap.xml":  true,
	"/tos":          true,
	"/privacy":      true,
	"/legal-notice": true,
}

var apiPrefixes = []string{"/api/", "/android/", "/desktop/"}

func suspendedAllowed(path string) bool {
	if suspendedAllowedPaths[path] {
		return true
	}
	for _, prefix := range suspendedAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isAPIPath(path string) bool {
	for _, prefix := range apiPrefixes {
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

func AccountStatusGate(renderSuspended gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCNSUser(c)
		if user == nil || UserAllows(user, accountstatus.UseSendly) {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if suspendedAllowed(path) {
			c.Next()
			return
		}
		if isAPIPath(path) || renderSuspended == nil || c.Request.Method != http.MethodGet {
			c.AbortWithStatusJSON(http.StatusForbidden, models.ErrorResponse{
				Error: models.ErrAccountSuspended.Message,
				Code:  models.ErrAccountSuspended.Code,
			})
			return
		}
		renderSuspended(c)
		c.Abort()
	}
}
