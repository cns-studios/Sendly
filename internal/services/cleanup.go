package services

import (
	"context"
	"log"
	"sync"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

// cleanupInterval is how often one replica of the deployment runs cleanup.
const cleanupInterval = 5 * time.Minute

// orphanMinAge protects files and chunk directories that were just created
// on another replica from the orphan sweep.
const orphanMinAge = 15 * time.Minute

type Cleanup struct {
	cfg      *config.Config
	db       *storage.Postgres
	fs       *storage.Filesystem
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewCleanup(cfg *config.Config, db *storage.Postgres, fs *storage.Filesystem) *Cleanup {
	return &Cleanup{
		cfg:      cfg,
		db:       db,
		fs:       fs,
		stopChan: make(chan struct{}),
	}
}

func (c *Cleanup) Start() {
	c.wg.Add(1)
	go c.run()
}

func (c *Cleanup) Stop() {
	close(c.stopChan)
	c.wg.Wait()
}

// run checks every minute whether cleanup is due. Every replica checks, but
// only one runs it per cleanupInterval.
func (c *Cleanup) run() {
	defer c.wg.Done()

	c.runIfDue()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.runIfDue()
		case <-c.stopChan:
			log.Println("Cleanup service stopping...")
			return
		}
	}
}

func (c *Cleanup) runIfDue() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := c.db.RunJob(ctx, "cleanup", cleanupInterval, c.performCleanup); err != nil {
		log.Printf("Cleanup: could not claim the cleanup run: %v", err)
	}
}

func (c *Cleanup) performCleanup(ctx context.Context) {
	log.Println("Starting cleanup cycle...")

	expiredCount, err := c.db.DeleteExpiredFiles(ctx)
	if err != nil {
		log.Printf("Error marking expired files as deleted: %v", err)
	} else if expiredCount > 0 {
		log.Printf("Marked %d expired files as deleted", expiredCount)
	}

	deletedFiles, err := c.getDeletedFiles(ctx)
	if err != nil {
		log.Printf("Error getting deleted files: %v", err)
	} else {
		for _, fileID := range deletedFiles {
			if err := c.fs.DeleteFile(fileID); err != nil {
				log.Printf("Error deleting file blob %s: %v", fileID, err)
			}
		}
		if len(deletedFiles) > 0 {
			log.Printf("Deleted %d file blobs", len(deletedFiles))
		}
	}

	orphanedCount, err := c.cleanupOrphanedChunks(ctx)
	if err != nil {
		log.Printf("Error cleaning up orphaned chunks: %v", err)
	} else if orphanedCount > 0 {
		log.Printf("Cleaned up %d orphaned chunk directories", orphanedCount)
	}

	if n, err := c.fs.CleanupStaleAssemblies(orphanMinAge); err != nil {
		log.Printf("Error cleaning up interrupted assemblies: %v", err)
	} else if n > 0 {
		log.Printf("Cleaned up %d interrupted assemblies", n)
	}

	if _, err := c.db.PurgeRateLimitCounters(ctx, c.longestRateLimitWindow()); err != nil {
		log.Printf("Error purging rate limit counters: %v", err)
	}
	if _, err := c.db.PurgeTokenRefreshResults(ctx); err != nil {
		log.Printf("Error purging token refresh results: %v", err)
	}

	RunRetention(ctx, c.cfg, c.db, time.Now())

	orphanedFiles, err := c.cleanupOrphanedFiles(ctx)
	if err != nil {
		log.Printf("Error cleaning up orphaned files: %v", err)
	} else if orphanedFiles > 0 {
		log.Printf("Cleaned up %d orphaned files", orphanedFiles)
	}

	log.Println("Cleanup cycle completed")
}

func (c *Cleanup) getDeletedFiles(ctx context.Context) ([]string, error) {

	files, err := c.db.GetDeletedFiles(ctx)
	if err != nil {
		return nil, err
	}

	fileIDs := make([]string, 0, len(files))
	for _, f := range files {
		if c.fs.FileExists(f.ID) {
			fileIDs = append(fileIDs, f.ID)
		}
	}

	return fileIDs, nil
}

func (c *Cleanup) cleanupOrphanedChunks(ctx context.Context) (int, error) {
	activeSessions, _, err := c.db.UploadSessionRefs(ctx)
	if err != nil {
		return 0, err
	}
	return c.fs.CleanupOrphanedChunks(activeSessions, orphanMinAge)
}

// cleanupOrphanedFiles deletes blobs that neither a file row nor an upload
// session (assembled, waiting for finalize) refers to.
func (c *Cleanup) cleanupOrphanedFiles(ctx context.Context) (int, error) {
	// Sessions are read before the directory, so a blob assembled in
	// between still has its session in the list.
	_, sessionFiles, err := c.db.UploadSessionRefs(ctx)
	if err != nil {
		return 0, err
	}

	diskFiles, err := c.fs.GetAllFileIDs()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, fileID := range diskFiles {
		if sessionFiles[fileID] {
			continue
		}

		_, err := c.db.GetFileForAdmin(ctx, fileID)
		if err != nil {
			if err == models.ErrFileNotFound {
				if err := c.fs.DeleteFile(fileID); err != nil {
					log.Printf("Error deleting orphaned file %s: %v", fileID, err)
					continue
				}
				cleaned++
			} else {
				log.Printf("Warning: DB error checking file %s, skipping: %v", fileID, err)
			}
			continue
		}
	}

	return cleaned, nil
}

func (c *Cleanup) longestRateLimitWindow() time.Duration {
	longest := c.cfg.RateLimitWindowSeconds
	for _, w := range []int64{c.cfg.StrictRateLimitWindowSeconds, c.cfg.DownloadRateLimitWindowSeconds} {
		if w > longest {
			longest = w
		}
	}
	return time.Duration(longest)*time.Second + time.Hour
}

func (c *Cleanup) ForceCleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c.performCleanup(ctx)
}

func (c *Cleanup) GetStats(ctx context.Context) (map[string]interface{}, error) {
	totalFiles, activeFiles, totalReports, totalSize, err := c.db.GetStats(ctx)
	if err != nil {
		return nil, err
	}

	diskSize, err := c.fs.GetTotalSize()
	if err != nil {
		return nil, err
	}

	activeSessions, pendingFiles, err := c.db.CountUploadSessions(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total_files_db":  totalFiles,
		"active_files_db": activeFiles,
		"total_reports":   totalReports,
		"total_size_db":   totalSize,
		"total_size_disk": diskSize,
		"active_sessions": activeSessions,
		"pending_files":   pendingFiles,
	}, nil
}
