package realtime

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	// Pings keep idle sockets alive through proxies (Cloudflare closes them
	// after 100 seconds of silence) and expose dead peers.
	pingEvery = 25 * time.Second
	pongWait  = 60 * time.Second
	// sendBuffer is how many events may queue for a slow client before it
	// is disconnected.
	sendBuffer = 32
	maxRead    = 64 << 10
)

// Conn is one client socket. All writes go through its own goroutine, so
// events, snapshots and pings never interleave on the wire.
type Conn struct {
	ws       *websocket.Conn
	out      chan []byte
	done     chan struct{}
	stopOnce sync.Once

	closeMu   sync.Mutex
	closeCode int
	closeText string
}

func newConn(ws *websocket.Conn) *Conn {
	return &Conn{ws: ws, out: make(chan []byte, sendBuffer), done: make(chan struct{})}
}

// WriteJSON queues v for the client.
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.send(data)
	return nil
}

func (c *Conn) send(data []byte) {
	select {
	case c.out <- data:
	case <-c.done:
	default:
		// The client isn't reading; drop it rather than block everyone else.
		c.closeWith(websocket.ClosePolicyViolation, "too slow")
	}
}

func (c *Conn) closeWith(code int, text string) {
	c.closeMu.Lock()
	if c.closeCode == 0 {
		c.closeCode, c.closeText = code, text
	}
	c.closeMu.Unlock()
	c.stopOnce.Do(func() { close(c.done) })
}

// serve runs until the socket closes: it reads (only to see pongs and the
// client hanging up) and writes queued messages and pings.
func (c *Conn) serve() {
	go func() {
		c.ws.SetReadLimit(maxRead)
		c.ws.SetReadDeadline(time.Now().Add(pongWait))
		c.ws.SetPongHandler(func(string) error {
			return c.ws.SetReadDeadline(time.Now().Add(pongWait))
		})
		for {
			if _, _, err := c.ws.ReadMessage(); err != nil {
				c.closeWith(0, "")
				return
			}
		}
	}()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	defer c.ws.Close()

	for {
		select {
		case data := <-c.out:
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				c.closeWith(0, "")
				return
			}
		case <-ping.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				c.closeWith(0, "")
				return
			}
		case <-c.done:
			c.closeMu.Lock()
			code, text := c.closeCode, c.closeText
			c.closeMu.Unlock()
			if code != 0 {
				c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
			}
			return
		}
	}
}
