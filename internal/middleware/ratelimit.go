package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

// RateLimitStore keeps request counters that every replica shares;
// implemented by *storage.Postgres.
type RateLimitStore interface {
	CheckRateLimit(ctx context.Context, key string, maxRequests int64, window time.Duration) (bool, error)
}

type RateLimiter struct {
	store     RateLimitStore
	maxPerMin int64
	window    time.Duration
	keyPrefix string
}

func NewRateLimiter(store RateLimitStore, maxPerMin int64, window time.Duration) *RateLimiter {
	return &RateLimiter{
		store:     store,
		maxPerMin: maxPerMin,
		window:    window,
		keyPrefix: "standard:",
	}
}

func (rl *RateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, unknown := rateLimitKey(c, rl.keyPrefix)
		if unknown {
			c.Next()
			return
		}

		allowed, err := rl.store.CheckRateLimit(c.Request.Context(), key, rl.maxPerMin, rl.window)
		if err != nil {

			c.Next()
			return
		}

		if !allowed {
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
				Error: models.ErrRateLimited.Message,
				Code:  models.ErrRateLimited.Code,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func NewStrictRateLimiter(store RateLimitStore, maxPerMin int64, window time.Duration) *RateLimiter {
	return &RateLimiter{
		store:     store,
		maxPerMin: maxPerMin,
		window:    window,
		keyPrefix: "strict:",
	}
}

func (rl *RateLimiter) StrictHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, unknown := rateLimitKey(c, rl.keyPrefix)
		if unknown {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error: "Could not determine client IP",
				Code:  "UNKNOWN_IP",
			})
			c.Abort()
			return
		}

		allowed, err := rl.store.CheckRateLimit(c.Request.Context(), key, rl.maxPerMin, rl.window)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
				Error: "Service temporarily unavailable",
				Code:  "SERVICE_ERROR",
			})
			c.Abort()
			return
		}

		if !allowed {
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
				Error: models.ErrRateLimited.Message,
				Code:  models.ErrRateLimited.Code,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

type DownloadRateLimiter struct {
	store     RateLimitStore
	maxPerMin int64
	window    time.Duration
	keyPrefix string
}

func NewDownloadRateLimiter(store RateLimitStore, maxPerMinute int64, window time.Duration) *DownloadRateLimiter {
	return &DownloadRateLimiter{
		store:     store,
		maxPerMin: maxPerMinute,
		window:    window,
		keyPrefix: "download:",
	}
}

func (drl *DownloadRateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, unknown := rateLimitKey(c, drl.keyPrefix)
		if unknown {
			c.Next()
			return
		}

		allowed, err := drl.store.CheckRateLimit(c.Request.Context(), key, drl.maxPerMin, drl.window)
		if err != nil {
			c.Next()
			return
		}

		if !allowed {
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
				Error: "Too many download requests, please slow down",
				Code:  "DOWNLOAD_RATE_LIMITED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func NewRateLimiterSet(cfg *config.Config, store RateLimitStore) (*RateLimiter, *RateLimiter, *DownloadRateLimiter) {
	standard := NewRateLimiter(store, cfg.RateLimitMaxPerMinute, time.Duration(cfg.RateLimitWindowSeconds)*time.Second)
	strict := NewStrictRateLimiter(store, cfg.StrictRateLimitMaxPerMinute, time.Duration(cfg.StrictRateLimitWindowSeconds)*time.Second)
	download := NewDownloadRateLimiter(store, cfg.DownloadRateLimitMaxPerMinute, time.Duration(cfg.DownloadRateLimitWindowSeconds)*time.Second)
	return standard, strict, download
}

func rateLimitKey(c *gin.Context, keyPrefix string) (string, bool) {
	route := c.FullPath()
	if route == "" {
		route = c.Request.URL.Path
	}

	user := GetCNSUser(c)
	if user != nil && user.ID > 0 {
		return fmt.Sprintf("%suser:%d:route:%s", keyPrefix, user.ID, route), false
	}

	ip := GetClientIP(c)
	if ip == "unknown" {
		return "", true
	}

	return fmt.Sprintf("%sip:%s:route:%s", keyPrefix, ip, route), false
}
