package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"sendly/internal/config"
)

type fakeRetentionStore struct {
	// remaining rows per category; each call removes up to batchSize.
	files, ips, reports, rejections int64
	calls                           map[string]int
	cutoffs                         map[string]time.Time
	failFiles                       bool
}

func newFakeRetentionStore() *fakeRetentionStore {
	return &fakeRetentionStore{calls: map[string]int{}, cutoffs: map[string]time.Time{}}
}

func take(remaining *int64, batch int) int64 {
	n := int64(batch)
	if *remaining < n {
		n = *remaining
	}
	*remaining -= n
	return n
}

func (f *fakeRetentionStore) PurgeDeletedFiles(_ context.Context, cutoff time.Time, batch int) (int64, error) {
	f.calls["files"]++
	f.cutoffs["files"] = cutoff
	if f.failFiles {
		return 0, errors.New("boom")
	}
	return take(&f.files, batch), nil
}

func (f *fakeRetentionStore) PurgeUploadIPs(_ context.Context, cutoff time.Time, batch int) (int64, error) {
	f.calls["ips"]++
	f.cutoffs["ips"] = cutoff
	return take(&f.ips, batch), nil
}

func (f *fakeRetentionStore) PurgeClosedReports(_ context.Context, cutoff, _ time.Time, batch int) (int64, error) {
	f.calls["reports"]++
	f.cutoffs["reports"] = cutoff
	return take(&f.reports, batch), nil
}

func (f *fakeRetentionStore) PurgeTunnelRejections(_ context.Context, cutoff, _ time.Time, batch int) (int64, error) {
	f.calls["rejections"]++
	f.cutoffs["rejections"] = cutoff
	return take(&f.rejections, batch), nil
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func retentionConfig() *config.Config {
	return &config.Config{
		FileRecordRetention:      day(30),
		UploadIPRetention:        day(31),
		ReportRetention:          day(90),
		TunnelRejectionRetention: day(32),
	}
}

func TestRunRetentionBatchesUntilDrained(t *testing.T) {
	store := newFakeRetentionStore()
	store.files = retentionBatchSize*2 + 10
	store.ips, store.reports, store.rejections = 3, 4, 5
	now := time.Now()

	result := RunRetention(context.Background(), retentionConfig(), store, now)

	if result != (RetentionResult{FileRecords: retentionBatchSize*2 + 10, UploadIPs: 3, Reports: 4, TunnelRejections: 5}) {
		t.Fatalf("unexpected result %+v", result)
	}
	if store.calls["files"] != 3 {
		t.Fatalf("file purge called %d times, want 3", store.calls["files"])
	}
	for key, want := range map[string]time.Duration{"files": day(30), "ips": day(31), "reports": day(90), "rejections": day(32)} {
		if got := now.Sub(store.cutoffs[key]); got != want {
			t.Errorf("%s cutoff is %v before now, want %v", key, got, want)
		}
	}

	// A second run over drained data is a no-op.
	if again := RunRetention(context.Background(), retentionConfig(), store, now); again != (RetentionResult{}) {
		t.Fatalf("second run deleted %+v", again)
	}
}

func TestRunRetentionZeroPeriodDisablesPurge(t *testing.T) {
	store := newFakeRetentionStore()
	store.files, store.ips, store.reports, store.rejections = 1, 1, 1, 1
	cfg := retentionConfig()
	cfg.UploadIPRetention = 0
	cfg.ReportRetention = 0

	result := RunRetention(context.Background(), cfg, store, time.Now())

	if store.calls["ips"] != 0 || store.calls["reports"] != 0 {
		t.Fatalf("disabled purges ran: %v", store.calls)
	}
	if result.FileRecords != 1 || result.TunnelRejections != 1 {
		t.Fatalf("enabled purges skipped: %+v", result)
	}
}

func TestRunRetentionErrorDoesNotStopOtherPurges(t *testing.T) {
	store := newFakeRetentionStore()
	store.failFiles = true
	store.ips = 2

	result := RunRetention(context.Background(), retentionConfig(), store, time.Now())

	if store.calls["files"] != 1 {
		t.Fatalf("failing purge retried %d times, want 1", store.calls["files"])
	}
	if result.UploadIPs != 2 {
		t.Fatalf("other purges did not run: %+v", result)
	}
}

func TestRunRetentionStopsWhenContextCancelled(t *testing.T) {
	store := newFakeRetentionStore()
	store.files = 10
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	RunRetention(ctx, retentionConfig(), store, time.Now())

	if len(store.calls) != 0 {
		t.Fatalf("purges ran on a cancelled context: %v", store.calls)
	}
}
