package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fanout stands in for Postgres NOTIFY: every hub receives every message.
type fanout struct {
	mu   sync.Mutex
	hubs []*Hub
	fail bool
}

func (f *fanout) Notify(_ context.Context, channel, payload string) error {
	if f.fail {
		return errors.New("database unavailable")
	}
	f.mu.Lock()
	hubs := append([]*Hub(nil), f.hubs...)
	f.mu.Unlock()
	for _, h := range hubs {
		h.deliverEnvelope(payload)
	}
	return nil
}

// connect opens a client socket registered under topic on hub.
func connect(t *testing.T, hub *Hub, topic string) *websocket.Conn {
	t.Helper()
	registered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Add(topic, ws)
		close(registered)
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	<-registered
	return client
}

func readText(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

func TestPublishReachesSocketsOnOtherReplicas(t *testing.T) {
	bus := &fanout{}
	replicaA, replicaB := NewHub(bus), NewHub(bus)
	bus.hubs = []*Hub{replicaA, replicaB}

	onB := connect(t, replicaB, "web:user:7")
	otherUser := connect(t, replicaB, "web:user:8")

	replicaA.Publish(context.Background(), "web:user:7", map[string]any{"type": "transfers_updated", "pending_count": 2})

	if got := readText(t, onB); got != `{"pending_count":2,"type":"transfers_updated"}` {
		t.Fatalf("got %s", got)
	}
	otherUser.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, data, err := otherUser.ReadMessage(); err == nil {
		t.Fatalf("another user's socket got %s", data)
	}
}

func TestPublishFallsBackToLocalDelivery(t *testing.T) {
	bus := &fanout{fail: true}
	hub := NewHub(bus)
	bus.hubs = []*Hub{hub}
	client := connect(t, hub, "desktop:key:1")

	hub.Publish(context.Background(), "desktop:key:1", map[string]string{"type": "new_file"})
	if got := readText(t, client); got != `{"type":"new_file"}` {
		t.Fatalf("got %s", got)
	}
}

func TestShutdownAsksClientsToReconnect(t *testing.T) {
	hub := NewHub(nil)
	client := connect(t, hub, "web:user:1")

	hub.Shutdown()

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := client.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseServiceRestart) {
		t.Fatalf("got %v, want close 1012 (service restart)", err)
	}
}
