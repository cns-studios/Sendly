package realtime

import (
	"context"
	"os"
	"testing"

	"sendly/internal/config"
	"sendly/internal/storage"
)

// Two replicas, each with its own LISTEN connection: an event published on
// one reaches a socket connected to the other through Postgres.
func TestLiveEventsCrossReplicasThroughPostgres(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.NewPostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	replicaA, replicaB := NewHub(db), NewHub(db)
	for _, hub := range []*Hub{replicaA, replicaB} {
		if err := Listen(ctx, cfg.PostgresDSN(), hub); err != nil {
			t.Fatal(err)
		}
	}

	client := connect(t, replicaB, "android:enrollment:abc")
	replicaA.Publish(ctx, "android:enrollment:abc", map[string]string{"type": "enrollment_status"})

	if got := readText(t, client); got != `{"type":"enrollment_status"}` {
		t.Fatalf("got %s", got)
	}
}
