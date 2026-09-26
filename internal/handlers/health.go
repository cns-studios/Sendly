package handlers

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	healthCheckTimeout = 2 * time.Second
	healthCacheTTL     = 5 * time.Second
)

// HealthCheck is one named dependency probe. Its error is logged, never
// returned to the client: /health is publicly reachable.
type HealthCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

type HealthHandler struct {
	checks []HealthCheck

	mu        sync.Mutex
	checkedAt time.Time
	healthy   bool
	results   map[string]string
}

func NewHealthHandler(checks ...HealthCheck) *HealthHandler {
	return &HealthHandler{checks: checks}
}

// Live only reports that the process answers requests.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// Health probes every dependency and answers 503 if any fails. The result is
// cached briefly so frequent probes don't repeat disk writes and pings.
func (h *HealthHandler) Health(c *gin.Context) {
	healthy, results := h.run(c.Request.Context())

	status, label := http.StatusOK, "healthy"
	if !healthy {
		status, label = http.StatusServiceUnavailable, "unhealthy"
	}
	c.JSON(status, gin.H{"status": label, "checks": results})
}

func (h *HealthHandler) run(ctx context.Context) (bool, map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.results != nil && time.Since(h.checkedAt) < healthCacheTTL {
		return h.healthy, h.results
	}

	healthy := true
	results := make(map[string]string, len(h.checks))
	for _, hc := range h.checks {
		checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
		err := hc.Check(checkCtx)
		cancel()
		if err != nil {
			healthy = false
			results[hc.Name] = "error"
			log.Printf("Health check %q failed: %v", hc.Name, err)
			continue
		}
		results[hc.Name] = "ok"
	}

	h.checkedAt, h.healthy, h.results = time.Now(), healthy, results
	return healthy, results
}
