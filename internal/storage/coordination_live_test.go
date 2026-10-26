package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sendly/internal/models"

	"github.com/lib/pq"
)

func newTestSession(t *testing.T, ctx context.Context, db *Postgres, chunks int) *models.UploadSession {
	t.Helper()
	s := &models.UploadSession{
		SessionID:    "sess-" + randomSuffix() + fmt.Sprint(testSeq.Add(1)),
		FileID:       fmt.Sprintf("f%016d", (time.Now().UnixNano()+testSeq.Add(1))%1e16),
		OriginalName: "x.bin", TotalSize: int64(chunks), TotalChunks: chunks, ChunkSize: 1,
		UploaderIP: "192.0.2.1", CreatedAt: time.Now(),
	}
	if err := db.CreateUploadSession(ctx, s, time.Hour); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DeleteUploadSession(context.Background(), s.SessionID) })
	return s
}

func TestLiveUploadSessionLifecycle(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	s := newTestSession(t, ctx, db, 2)

	if _, err := db.StartAssembly(ctx, s.SessionID, time.Minute); !errors.Is(err, models.ErrUploadIncomplete) {
		t.Fatalf("complete before all chunks: err = %v, want ErrUploadIncomplete", err)
	}

	// A chunk recorded twice (a client retrying after a lost response) counts once.
	for _, idx := range []int{0, 0, 1} {
		if err := db.RecordChunk(ctx, s.SessionID, idx, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := db.CountUploadedChunks(ctx, s.SessionID); n != 2 {
		t.Fatalf("uploaded chunks = %d, want 2", n)
	}

	started, err := db.StartAssembly(ctx, s.SessionID, time.Minute)
	if err != nil || started.State != models.UploadStateAssembling {
		t.Fatalf("StartAssembly = %+v, %v", started, err)
	}
	// Repeating complete leaves the session as it is.
	again, err := db.StartAssembly(ctx, s.SessionID, time.Minute)
	if err != nil || again.State != models.UploadStateAssembling || !again.ExpiresAt.Equal(started.ExpiresAt) {
		t.Fatalf("repeated StartAssembly = %+v, %v", again, err)
	}
	if err := db.RecordChunk(ctx, s.SessionID, 1, time.Hour); !errors.Is(err, models.ErrUploadNotAccepting) {
		t.Fatalf("chunk after complete: err = %v, want ErrUploadNotAccepting", err)
	}

	// Only one replica gets the lease.
	if got, err := db.ClaimAssembly(ctx, s.SessionID, "pod-a", time.Minute); err != nil || got == nil {
		t.Fatalf("pod-a claim = %v, %v", got, err)
	}
	if got, err := db.ClaimAssembly(ctx, s.SessionID, "pod-b", time.Minute); err != nil || got != nil {
		t.Fatalf("pod-b claim while leased = %v, %v; want none", got, err)
	}
	if ids, _ := db.ListOrphanedAssemblies(ctx, 1000); contains(ids, s.SessionID) {
		t.Fatal("a leased assembly is listed as orphaned")
	}

	// pod-a dies: once its lease runs out, pod-b takes over and pod-a's
	// late result is fenced off.
	if _, err := db.db.ExecContext(ctx, `UPDATE upload_sessions SET assembly_lease_until = NOW() - INTERVAL '1 second' WHERE session_id = $1`, s.SessionID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := db.ListOrphanedAssemblies(ctx, 1000); !contains(ids, s.SessionID) {
		t.Fatal("an assembly with an expired lease is not listed as orphaned")
	}
	if got, err := db.ClaimAssembly(ctx, s.SessionID, "pod-b", time.Minute); err != nil || got == nil {
		t.Fatalf("pod-b takeover = %v, %v", got, err)
	}
	if ok, _ := db.FinishAssembly(ctx, s.SessionID, "pod-a", time.Minute); ok {
		t.Fatal("pod-a finished an assembly it no longer owns")
	}
	if ok, _ := db.RenewAssembly(ctx, s.SessionID, "pod-a", time.Minute); ok {
		t.Fatal("pod-a renewed a lease it no longer owns")
	}
	if ok, err := db.FinishAssembly(ctx, s.SessionID, "pod-b", time.Minute); err != nil || !ok {
		t.Fatalf("pod-b finish = %v, %v", ok, err)
	}
	got, err := db.GetUploadSession(ctx, s.SessionID)
	if err != nil || got.State != models.UploadStateAssembled {
		t.Fatalf("after finish: %+v, %v", got, err)
	}
}

func TestLiveExpiredUploadSessionsAreTakenOnce(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	expired := newTestSession(t, ctx, db, 1)
	leased := newTestSession(t, ctx, db, 1)
	live := newTestSession(t, ctx, db, 1)

	if _, err := db.db.ExecContext(ctx, `UPDATE upload_sessions SET expires_at = NOW() - INTERVAL '1 minute' WHERE session_id = ANY($1)`,
		pq.Array([]string{expired.SessionID, leased.SessionID})); err != nil {
		t.Fatal(err)
	}
	// An assembly in progress outlives its expiry while its lease holds.
	if _, err := db.db.ExecContext(ctx, `UPDATE upload_sessions SET state = 'assembling', assembly_owner = 'pod-a', assembly_lease_until = NOW() + INTERVAL '1 minute' WHERE session_id = $1`, leased.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUploadSession(ctx, expired.SessionID); !errors.Is(err, models.ErrSessionNotFound) {
		t.Fatalf("expired session lookup: err = %v, want ErrSessionNotFound", err)
	}

	// Two replicas cleaning up at once never take the same session.
	var mu sync.Mutex
	taken := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessions, err := db.TakeExpiredUploadSessions(ctx, 1000)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, s := range sessions {
				taken[s.SessionID]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if taken[expired.SessionID] != 1 {
		t.Fatalf("expired session taken %d times, want 1", taken[expired.SessionID])
	}
	if taken[leased.SessionID] != 0 || taken[live.SessionID] != 0 {
		t.Fatalf("took sessions that must stay: %v", taken)
	}
}

func TestLiveRateLimitIsShared(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	key := "test:" + randomSuffix()
	for i := 1; i <= 4; i++ {
		allowed, err := db.CheckRateLimit(ctx, key, 3, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if allowed != (i <= 3) {
			t.Fatalf("request %d allowed = %v", i, allowed)
		}
	}
}

func TestLiveSharedRefreshRunsOnce(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	tokenID := []byte("token-" + randomSuffix())

	var calls atomic.Int32
	var wg sync.WaitGroup
	results := make([][]byte, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sealed, _, err := db.SharedRefresh(ctx, tokenID, time.Minute, func() ([]byte, error) {
				calls.Add(1)
				time.Sleep(100 * time.Millisecond)
				return []byte("sealed-result"), nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i] = sealed
		}(i)
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("refresh ran %d times, want 1", calls.Load())
	}
	for i, r := range results {
		if string(r) != "sealed-result" {
			t.Fatalf("caller %d got %q", i, r)
		}
	}

	// A failed refresh is not stored, so the next caller tries again.
	failID := []byte("fail-" + randomSuffix())
	if _, _, err := db.SharedRefresh(ctx, failID, time.Minute, func() ([]byte, error) { return nil, errors.New("upstream down") }); err == nil {
		t.Fatal("want the refresh error")
	}
	if sealed, cached, err := db.SharedRefresh(ctx, failID, time.Minute, func() ([]byte, error) { return []byte("ok"), nil }); err != nil || cached || string(sealed) != "ok" {
		t.Fatalf("retry after failure = %q, cached=%v, %v", sealed, cached, err)
	}
}

func TestLiveRunJobOncePerInterval(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	name := "test-job-" + randomSuffix()

	var runs atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.RunJob(ctx, name, time.Hour, func(context.Context) {
				runs.Add(1)
				time.Sleep(50 * time.Millisecond)
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("job ran %d times, want 1", runs.Load())
	}

	if _, err := db.db.ExecContext(ctx, `UPDATE job_runs SET last_run_at = NOW() - INTERVAL '2 hours' WHERE name = $1`, name); err != nil {
		t.Fatal(err)
	}
	if ran, err := db.RunJob(ctx, name, time.Hour, func(context.Context) {}); err != nil || !ran {
		t.Fatalf("due job: ran = %v, %v", ran, err)
	}
	db.db.ExecContext(ctx, `DELETE FROM job_runs WHERE name = $1`, name)
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
