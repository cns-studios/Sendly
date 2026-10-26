// Package realtime pushes events to WebSocket clients across replicas.
//
// Each replica keeps the sockets connected to it in a Hub, grouped by topic
// (a user, an API key, an enrollment). Publishing an event sends it through
// Postgres NOTIFY; every replica's Listener receives it and hands it to its
// Hub, which writes it to the matching local sockets. A client therefore
// gets its events no matter which replica its socket landed on.
package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// maxNotifyPayload stays under Postgres' 8000 byte NOTIFY limit.
const maxNotifyPayload = 7900

// Publisher sends a message to every replica, this one included.
type Publisher interface {
	Notify(ctx context.Context, channel, payload string) error
}

// Channel is the Postgres NOTIFY channel events travel on.
const Channel = "sendly_events"

type envelope struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

type Hub struct {
	pub Publisher

	mu      sync.Mutex
	topics  map[string]map[*Conn]struct{}
	closing bool
}

// NewHub returns a hub that publishes through pub. With a nil pub, events
// only reach sockets on this replica.
func NewHub(pub Publisher) *Hub {
	return &Hub{pub: pub, topics: make(map[string]map[*Conn]struct{})}
}

// Add registers an upgraded socket under topic and starts serving it. The
// returned Conn is safe for concurrent writes; the socket is removed from
// the hub once it closes.
func (h *Hub) Add(topic string, ws *websocket.Conn) *Conn {
	c := newConn(ws)

	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		c.closeWith(websocket.CloseServiceRestart, "server restarting")
		return c
	}
	if h.topics[topic] == nil {
		h.topics[topic] = make(map[*Conn]struct{})
	}
	h.topics[topic][c] = struct{}{}
	h.mu.Unlock()

	go func() {
		c.serve()
		h.mu.Lock()
		delete(h.topics[topic], c)
		if len(h.topics[topic]) == 0 {
			delete(h.topics, topic)
		}
		h.mu.Unlock()
	}()
	return c
}

// Publish sends payload to every socket under topic on every replica. If
// the event can't go through Postgres, it at least reaches this replica's
// sockets.
func (h *Hub) Publish(ctx context.Context, topic string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("realtime: marshal event for %s: %v", topic, err)
		return
	}
	if h.pub != nil {
		msg, err := json.Marshal(envelope{Topic: topic, Payload: data})
		if err == nil && len(msg) <= maxNotifyPayload {
			notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			err = h.pub.Notify(notifyCtx, Channel, string(msg))
			cancel()
			if err == nil {
				return
			}
		}
		if err == nil {
			log.Printf("realtime: event for %s is %d bytes, too large for NOTIFY; delivering locally only", topic, len(msg))
		} else {
			log.Printf("realtime: publish to other replicas failed, delivering locally only: %v", err)
		}
	}
	h.Deliver(topic, data)
}

// Deliver writes an already-encoded event to this replica's sockets under topic.
func (h *Hub) Deliver(topic string, data []byte) {
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.topics[topic]))
	for c := range h.topics[topic] {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.send(data)
	}
}

// deliverEnvelope hands a NOTIFY payload to Deliver.
func (h *Hub) deliverEnvelope(raw string) {
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		log.Printf("realtime: ignoring malformed event: %v", err)
		return
	}
	h.Deliver(env.Topic, env.Payload)
}

// DropAll closes every socket with code. Clients reconnect (to whichever
// replica is available) and reload their state, which is how they catch up
// on events they may have missed.
func (h *Hub) DropAll(code int, reason string) {
	h.mu.Lock()
	var conns []*Conn
	for _, set := range h.topics {
		for c := range set {
			conns = append(conns, c)
		}
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.closeWith(code, reason)
	}
}

// Shutdown refuses new sockets and closes the open ones with "service
// restart", telling clients to reconnect elsewhere right away.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	h.DropAll(websocket.CloseServiceRestart, "server restarting")
}
