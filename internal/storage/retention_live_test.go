package storage

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"sendly/internal/config"
)

func liveRetentionDB(t *testing.T) (context.Context, *Postgres) {
	t.Helper()
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MigrationsDir = "../../db/migrations"
	db, err := NewPostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		db.Close()
	})
	if err := db.RunMigrations(ctx, cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func count(t *testing.T, ctx context.Context, db *Postgres, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.db.GetContext(ctx, &n, query, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

var testSeq atomic.Int64

func randomSuffix() string { return fmt.Sprintf("%012d", time.Now().UnixNano()%1e12) }

// testCode returns a unique 12-digit numeric code.
func testCode() string { return fmt.Sprintf("%012d", (time.Now().UnixNano()+testSeq.Add(1))%1e12) }

func insertTestFile(t *testing.T, ctx context.Context, db *Postgres, id string, deleted bool, deletedAt, expiresAt time.Time) {
	t.Helper()
	var del any
	if deleted {
		del = deletedAt
	}
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO files (id, numeric_code, original_name, size_bytes, expires_at, is_deleted, deleted_at)
		VALUES ($1, $2, 'x', 1, $3, $4, $5)
	`, id, testCode(), expiresAt, deleted, del); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO file_key_envelopes (file_id, wrapped_dek, dek_wrap_alg) VALUES ($1, '\x01', 'test')
	`, id); err != nil {
		t.Fatal(err)
	}
}

func TestLivePurgeDeletedFiles(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	sfx := randomSuffix()
	now := time.Now()
	old, live, recent, expiredOnly := "rtold"+sfx, "rtlive"+sfx, "rtnew"+sfx, "rtexp"+sfx
	insertTestFile(t, ctx, db, old, true, now.Add(-40*24*time.Hour), now.Add(time.Hour))
	insertTestFile(t, ctx, db, recent, true, now.Add(-5*24*time.Hour), now.Add(time.Hour))
	insertTestFile(t, ctx, db, live, false, time.Time{}, now.Add(-100*24*time.Hour)) // expired but not yet soft-deleted
	insertTestFile(t, ctx, db, expiredOnly, true, now.Add(-40*24*time.Hour), now.Add(-41*24*time.Hour))
	t.Cleanup(func() {
		db.db.Exec(`DELETE FROM files WHERE id = ANY($1)`, "{"+old+","+live+","+recent+","+expiredOnly+"}")
	})

	cutoff := now.Add(-30 * 24 * time.Hour)
	n, err := db.PurgeDeletedFiles(ctx, cutoff, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("purged %d rows, want at least the 2 old ones", n)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM files WHERE id = ANY($1)`, "{"+old+","+expiredOnly+"}"); got != 0 {
		t.Fatalf("old soft-deleted files survived: %d", got)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM file_key_envelopes WHERE file_id = ANY($1)`, "{"+old+","+expiredOnly+"}"); got != 0 {
		t.Fatalf("envelopes of purged files survived: %d", got)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM files WHERE id = ANY($1)`, "{"+live+","+recent+"}"); got != 2 {
		t.Fatalf("live or recently deleted files were touched: %d of 2 left", got)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM file_key_envelopes WHERE file_id = ANY($1)`, "{"+live+","+recent+"}"); got != 2 {
		t.Fatalf("envelopes of kept files were touched: %d of 2 left", got)
	}

	// Idempotent: nothing of ours is left to delete.
	if _, err := db.PurgeDeletedFiles(ctx, cutoff, 1000); err != nil {
		t.Fatal(err)
	}
}

func TestLivePurgeDeletedFilesRespectsBatchSize(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	sfx := randomSuffix()
	now := time.Now()
	ids := []string{"rtb1" + sfx, "rtb2" + sfx, "rtb3" + sfx}
	for _, id := range ids {
		insertTestFile(t, ctx, db, id, true, now.Add(-400*24*time.Hour), now.Add(-401*24*time.Hour))
	}
	t.Cleanup(func() { db.db.Exec(`DELETE FROM files WHERE id = ANY($1)`, "{"+ids[0]+","+ids[1]+","+ids[2]+"}") })
	ours := "{" + ids[0] + "," + ids[1] + "," + ids[2] + "}"

	// Drain everything else so the batch limit is observable on our rows.
	for {
		n, err := db.PurgeDeletedFiles(ctx, now.Add(-30*24*time.Hour), 1)
		if err != nil {
			t.Fatal(err)
		}
		if n > 1 {
			t.Fatalf("batch of 1 deleted %d rows", n)
		}
		if n == 0 {
			break
		}
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM files WHERE id = ANY($1)`, ours); got != 0 {
		t.Fatalf("%d rows left after draining", got)
	}
}

func TestLivePurgeUploadIPs(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	sfx := randomSuffix()
	oldIP, newIP := "198.51.100.1-"+sfx, "198.51.100.2-"+sfx
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO uploads_by_ip (ip, uploaded_bytes, updated_at) VALUES ($1, 1, NOW() - INTERVAL '40 days'), ($2, 1, NOW())
	`, oldIP, newIP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.db.Exec(`DELETE FROM uploads_by_ip WHERE ip IN ($1, $2)`, oldIP, newIP) })

	if _, err := db.PurgeUploadIPs(ctx, time.Now().Add(-30*24*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM uploads_by_ip WHERE ip = $1`, oldIP); got != 0 {
		t.Fatal("old IP row survived")
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM uploads_by_ip WHERE ip = $1`, newIP); got != 1 {
		t.Fatal("recent IP row was deleted")
	}
}

func TestLivePurgeClosedReports(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	sfx := randomSuffix()
	now := time.Now()
	liveFile, deletedFile, goneFile := "rtrl"+sfx, "rtrd"+sfx, "rtrg"+sfx
	insertTestFile(t, ctx, db, liveFile, false, time.Time{}, now.Add(time.Hour))
	insertTestFile(t, ctx, db, deletedFile, true, now.Add(-time.Hour), now.Add(time.Hour))
	t.Cleanup(func() {
		db.db.Exec(`DELETE FROM reports WHERE file_id = ANY($1)`, "{"+liveFile+","+deletedFile+","+goneFile+"}")
		db.db.Exec(`DELETE FROM files WHERE id = ANY($1)`, "{"+liveFile+","+deletedFile+"}")
	})
	old := now.Add(-100 * 24 * time.Hour)
	for _, r := range []struct {
		file string
		at   time.Time
	}{{liveFile, old}, {deletedFile, old}, {goneFile, old}, {deletedFile, now}} {
		if _, err := db.db.ExecContext(ctx, `INSERT INTO reports (file_id, reporter_ip, created_at) VALUES ($1, 'x', $2)`, r.file, r.at); err != nil {
			t.Fatalf("insert report (reports must not require the file row): %v", err)
		}
	}

	if _, err := db.PurgeClosedReports(ctx, now.Add(-90*24*time.Hour), now, 1000); err != nil {
		t.Fatal(err)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM reports WHERE file_id = $1`, liveFile); got != 1 {
		t.Errorf("report on live file: %d left, want 1", got)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM reports WHERE file_id = $1`, goneFile); got != 0 {
		t.Errorf("old report on missing file: %d left, want 0", got)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM reports WHERE file_id = $1`, deletedFile); got != 1 {
		t.Errorf("deleted file reports: %d left, want only the recent one", got)
	}
}

func TestLivePurgeTunnelRejections(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	sfx := randomSuffix()
	now := time.Now()
	openID, endedID := "00000000-0000-4000-8000-"+sfx, "00000000-0000-4000-9000-"+sfx
	for _, tn := range []struct{ id, status string }{{openID, "active"}, {endedID, "ended"}} {
		if _, err := db.db.ExecContext(ctx, `
			INSERT INTO tunnels (id, code, initiator_cns_user_id, duration_minutes, status, expires_at)
			VALUES ($1, $2, 1, 60, $3, $4)
		`, tn.id, "rt-"+tn.status+sfx, tn.status, now.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO tunnel_rejections (tunnel_id, created_at) VALUES ($1, $2)`, tn.id, now.Add(-60*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.db.Exec(`DELETE FROM tunnels WHERE id IN ($1, $2)`, openID, endedID) })

	if _, err := db.PurgeTunnelRejections(ctx, now.Add(-30*24*time.Hour), now, 1000); err != nil {
		t.Fatal(err)
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM tunnel_rejections WHERE tunnel_id = $1`, endedID); got != 0 {
		t.Error("old rejection of an ended tunnel survived")
	}
	if got := count(t, ctx, db, `SELECT COUNT(*) FROM tunnel_rejections WHERE tunnel_id = $1`, openID); got != 1 {
		t.Error("rejection of an open tunnel was deleted")
	}
}
