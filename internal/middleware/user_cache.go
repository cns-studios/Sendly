package middleware

import (
	"context"

	"sendly/internal/services"

	"github.com/gin-gonic/gin"
)

// UserCacheSyncMiddleware triggers UserCache.SyncIfStale for the CNS user
// CNSAuthMiddleware just resolved, if any. It runs detached from the request
// (its own background context, not c.Request.Context()) so a stale-cache
// sync never adds latency to the response; SyncIfStale is itself TTL-gated
// so this is a fast local DB read on every other request.
func UserCacheSyncMiddleware(userCache *services.UserCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCNSUser(c)
		if user == nil {
			c.Next()
			return
		}
		// Use the token CNSAuthMiddleware just validated (and possibly
		// refreshed), not the raw request cookie: re-reading the cookie
		// here would risk using a token that was rotated out earlier in
		// this same middleware chain.
		if token := GetCNSAccessToken(c); token != "" {
			go userCache.SyncIfStale(context.Background(), int64(user.ID), token)
		}
		c.Next()
	}
}
