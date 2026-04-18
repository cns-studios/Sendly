package middleware

import (
	"net/http"
	"time"

	"shareit/internal/config"
	"shareit/internal/models"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

type RateLimiter struct {
	redis     *storage.Redis
	maxPerMin int64
	window    time.Duration
	keyPrefix string
}

func NewRateLimiter(redis *storage.Redis, maxPerMin int64, window time.Duration) *RateLimiter {
	return &RateLimiter{
		redis:     redis,
		maxPerMin: maxPerMin,
		window:    window,
		keyPrefix: "standard:",
	}
}

func (rl *RateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := GetClientIP(c)

		if ip == "unknown" {
			c.Next()
			return
		}

		allowed, err := rl.redis.CheckRateLimit(c.Request.Context(), rl.keyPrefix+ip, rl.maxPerMin, rl.window)
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

func NewStrictRateLimiter(redis *storage.Redis, maxPerMin int64, window time.Duration) *RateLimiter {
	return &RateLimiter{
		redis:     redis,
		maxPerMin: maxPerMin,
		window:    window,
		keyPrefix: "strict:",
	}
}

func (rl *RateLimiter) StrictHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := GetClientIP(c)

		if ip == "unknown" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error: "Could not determine client IP",
				Code:  "UNKNOWN_IP",
			})
			c.Abort()
			return
		}

		allowed, err := rl.redis.CheckRateLimit(c.Request.Context(), rl.keyPrefix+ip, rl.maxPerMin, rl.window)
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
	redis     *storage.Redis
	maxPerMin int64
	window    time.Duration
	keyPrefix string
}

func NewDownloadRateLimiter(redis *storage.Redis, maxPerMinute int64, window time.Duration) *DownloadRateLimiter {
	return &DownloadRateLimiter{
		redis:     redis,
		maxPerMin: maxPerMinute,
		window:    window,
		keyPrefix: "download:",
	}
}

func (drl *DownloadRateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := GetClientIP(c)

		if ip == "unknown" {
			c.Next()
			return
		}

		allowed, err := drl.redis.CheckRateLimit(c.Request.Context(), drl.keyPrefix+ip, drl.maxPerMin, drl.window)
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

func NewRateLimiterSet(cfg *config.Config, redis *storage.Redis) (*RateLimiter, *RateLimiter, *DownloadRateLimiter) {
	standard := NewRateLimiter(redis, cfg.RateLimitMaxPerMinute, time.Duration(cfg.RateLimitWindowSeconds)*time.Second)
	strict := NewStrictRateLimiter(redis, cfg.StrictRateLimitMaxPerMinute, time.Duration(cfg.StrictRateLimitWindowSeconds)*time.Second)
	download := NewDownloadRateLimiter(redis, cfg.DownloadRateLimitMaxPerMinute, time.Duration(cfg.DownloadRateLimitWindowSeconds)*time.Second)
	return standard, strict, download
}
