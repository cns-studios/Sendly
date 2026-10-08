package services

import (
	"context"
	"log"
	"time"

	"sendly/internal/config"
)

const (
	retentionBatchSize = 500
	// Bounds one cycle's work; a backlog drains over the following cycles.
	retentionMaxBatches = 100
)

// RetentionStore is the part of the database the retention purge needs.
type RetentionStore interface {
	PurgeDeletedFiles(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
	PurgeUploadIPs(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
	PurgeClosedReports(ctx context.Context, cutoff, now time.Time, batchSize int) (int64, error)
	PurgeTunnelRejections(ctx context.Context, cutoff, now time.Time, batchSize int) (int64, error)
}

// RetentionResult counts the rows deleted per category.
type RetentionResult struct {
	FileRecords      int64
	UploadIPs        int64
	Reports          int64
	TunnelRejections int64
}

// RunRetention hard-deletes rows that are past their retention period. A zero
// period disables that purge. A failing purge is logged and the others still
// run. Only counts are logged.
func RunRetention(ctx context.Context, cfg *config.Config, store RetentionStore, now time.Time) RetentionResult {
	var result RetentionResult

	purge := func(name string, period time.Duration, run func(cutoff time.Time) (int64, error)) int64 {
		if period <= 0 {
			return 0
		}
		cutoff := now.Add(-period)
		var total int64
		for i := 0; i < retentionMaxBatches; i++ {
			if ctx.Err() != nil {
				break
			}
			n, err := run(cutoff)
			total += n
			if err != nil {
				log.Printf("Retention: error purging %s: %v", name, err)
				break
			}
			if n < retentionBatchSize {
				break
			}
		}
		if total > 0 {
			log.Printf("Retention: deleted %d %s", total, name)
		}
		return total
	}

	result.FileRecords = purge("file records", cfg.FileRecordRetention, func(cutoff time.Time) (int64, error) {
		return store.PurgeDeletedFiles(ctx, cutoff, retentionBatchSize)
	})
	result.Reports = purge("closed reports", cfg.ReportRetention, func(cutoff time.Time) (int64, error) {
		return store.PurgeClosedReports(ctx, cutoff, now, retentionBatchSize)
	})
	result.UploadIPs = purge("upload IP records", cfg.UploadIPRetention, func(cutoff time.Time) (int64, error) {
		return store.PurgeUploadIPs(ctx, cutoff, retentionBatchSize)
	})
	result.TunnelRejections = purge("tunnel rejections", cfg.TunnelRejectionRetention, func(cutoff time.Time) (int64, error) {
		return store.PurgeTunnelRejections(ctx, cutoff, now, retentionBatchSize)
	})
	return result
}
