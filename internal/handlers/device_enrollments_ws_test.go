package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// A cookie queued before the upgrade (e.g. a refreshed session from
// CNSAuthMiddleware) must reach the client in the 101 handshake.
func TestUpgradeResponseCookiesReachHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", func(c *gin.Context) {
		c.SetCookie("refresh_token", "rotated", 60, "/", "", false, true)
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, upgradeResponseCookies(c))
		if err != nil {
			return
		}
		conn.Close()
	})
	server := httptest.NewServer(router)
	defer server.Close()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var found bool
	for _, cookie := range (&http.Response{Header: resp.Header}).Cookies() {
		if cookie.Name == "refresh_token" && cookie.Value == "rotated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("handshake Set-Cookie = %q, want refresh_token=rotated", resp.Header.Values("Set-Cookie"))
	}
}

func TestUpgradeResponseCookiesNoneQueued(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := upgradeResponseCookies(c); got != nil {
		t.Fatalf("got %v, want nil header", got)
	}
}
