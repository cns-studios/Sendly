package realtime

import (
	"context"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lib/pq"
)

// Listen receives events from every replica over Postgres LISTEN and hands
// them to hub until ctx ends. The listener reconnects by itself, for example
// after a database failover; events sent while it was away are lost, so the
// hub's sockets are then closed and their clients reload on reconnecting.
func Listen(ctx context.Context, dsn string, hub *Hub) error {
	listener := pq.NewListener(dsn, time.Second, 30*time.Second, func(ev pq.ListenerEventType, err error) {
		switch ev {
		case pq.ListenerEventDisconnected:
			log.Printf("realtime: listener disconnected: %v", err)
		case pq.ListenerEventReconnected:
			log.Println("realtime: listener reconnected")
		case pq.ListenerEventConnectionAttemptFailed:
			log.Printf("realtime: listener connection attempt failed: %v", err)
		}
	})
	if err := listener.Listen(Channel); err != nil {
		listener.Close()
		return err
	}

	go func() {
		defer listener.Close()
		check := time.NewTicker(90 * time.Second)
		defer check.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case n := <-listener.Notify:
				if n == nil {
					// Sent after a reconnect: anything in between was missed.
					hub.DropAll(websocket.CloseServiceRestart, "resync")
					continue
				}
				hub.deliverEnvelope(n.Extra)
			case <-check.C:
				go listener.Ping()
			}
		}
	}()
	return nil
}
