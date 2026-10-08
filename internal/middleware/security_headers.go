package middleware

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// ContentSecurityPolicy builds the CSP sent with every response. All scripts,
// fonts and stylesheets must be served from our own origin. Inline style
// attributes are still used by the templates, hence 'unsafe-inline' for styles
// only; scripts never allow inline code. Avatars are served by the CNS
// identity provider, so images may come from any https host.
func ContentSecurityPolicy(baseURL string) string {
	connect := []string{"'self'", "blob:"}
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		connect = append(connect, scheme+"://"+u.Host)
	}

	directives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self'",
		"img-src 'self' data: blob: https:",
		"connect-src " + strings.Join(connect, " "),
		"manifest-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}
	return strings.Join(directives, "; ")
}

// SecurityHeaders sets the Content-Security-Policy header on every response.
func SecurityHeaders(baseURL string) gin.HandlerFunc {
	policy := ContentSecurityPolicy(baseURL)
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", policy)
		c.Next()
	}
}
