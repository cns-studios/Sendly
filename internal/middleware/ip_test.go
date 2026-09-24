package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

func clientIPFor(t *testing.T, cfg *config.Config, remoteAddr string, headers map[string]string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	router.Use(NewIPMiddleware(cfg).Handler())
	var got string
	router.GET("/", func(c *gin.Context) { got = GetClientIP(c) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	router.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeers(t *testing.T) {
	cfg := &config.Config{BehindCloudflare: true, TrustedProxies: []string{"10.0.0.0/8"}}
	got := clientIPFor(t, cfg, "203.0.113.7:4444", map[string]string{
		"CF-Connecting-IP": "198.51.100.1",
		"X-Forwarded-For":  "198.51.100.2",
		"X-Real-IP":        "198.51.100.3",
	})
	if got != "203.0.113.7" {
		t.Fatalf("expected direct peer IP, got %q", got)
	}
}

func TestClientIPWithoutTrustedProxiesUsesPeer(t *testing.T) {
	got := clientIPFor(t, &config.Config{}, "203.0.113.7:4444", map[string]string{
		"X-Forwarded-For": "198.51.100.2",
	})
	if got != "203.0.113.7" {
		t.Fatalf("expected direct peer IP, got %q", got)
	}
}

func TestClientIPUsesCloudflareHeaderFromTrustedPeer(t *testing.T) {
	cfg := &config.Config{BehindCloudflare: true, TrustedProxies: []string{"10.0.0.0/8"}}
	got := clientIPFor(t, cfg, "10.1.2.3:4444", map[string]string{"CF-Connecting-IP": "198.51.100.1"})
	if got != "198.51.100.1" {
		t.Fatalf("expected CF-Connecting-IP, got %q", got)
	}
}

func TestClientIPUsesForwardedForFromTrustedPeer(t *testing.T) {
	cfg := &config.Config{TrustedProxies: []string{"10.1.2.3"}}
	got := clientIPFor(t, cfg, "10.1.2.3:4444", map[string]string{"X-Forwarded-For": "198.51.100.2"})
	if got != "198.51.100.2" {
		t.Fatalf("expected X-Forwarded-For client, got %q", got)
	}
}
