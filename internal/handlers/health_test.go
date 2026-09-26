package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func serveHealth(h *HealthHandler) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", h.Health)
	router.GET("/livez", h.Live)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	return w
}

func TestHealthAllChecksPass(t *testing.T) {
	h := NewHealthHandler(HealthCheck{Name: "postgres", Check: func(context.Context) error { return nil }})
	w := serveHealth(h)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"postgres":"ok"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestHealthFailingCheckIs503AndHidesError(t *testing.T) {
	h := NewHealthHandler(
		HealthCheck{Name: "postgres", Check: func(context.Context) error { return nil }},
		HealthCheck{Name: "redis", Check: func(context.Context) error { return errors.New("dial tcp 10.0.0.5:6379: secret detail") }},
	)
	w := serveHealth(h)
	body := w.Body.String()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", w.Code)
	}
	if !strings.Contains(body, `"redis":"error"`) || !strings.Contains(body, `"postgres":"ok"`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "secret detail") {
		t.Fatalf("error detail leaked to client: %s", body)
	}
}

func TestHealthResultIsCached(t *testing.T) {
	calls := 0
	h := NewHealthHandler(HealthCheck{Name: "storage", Check: func(context.Context) error { calls++; return nil }})
	serveHealth(h)
	serveHealth(h)
	if calls != 1 {
		t.Fatalf("check ran %d times; want 1 (cached)", calls)
	}
}
