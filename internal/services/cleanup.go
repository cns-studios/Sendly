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

type Cleanup struct {
	cfg      *config.Config
	db       *storage.Postgres
	redis    *storage.Redis
	fs       *storage.Filesystem
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewCleanup(cfg *config.Config, db *storage.Postgres, redis *storage.Redis, fs *storage.Filesystem) *Cleanup {
	return &Cleanup{
		cfg:      cfg,
		db:       db,
		redis:    redis,
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

func (c *Cleanup) run() {
	defer c.wg.Done()

	c.performCleanup()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.performCleanup()
		case <-c.stopChan:
			log.Println("Cleanup service stopping...")
			return
		}
	}
}

func (c *Cleanup) performCleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

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

	activeSessions, err := c.redis.GetAllActiveSessions(ctx)
	if err != nil {
		return 0, err
	}

	activeMap := make(map[string]bool)
	for _, sessionID := range activeSessions {
		activeMap[sessionID] = true
	}

	return c.fs.CleanupOrphanedChunks(activeMap)
}

func (c *Cleanup) cleanupOrphanedFiles(ctx context.Context) (int, error) {

	diskFiles, err := c.fs.GetAllFileIDs()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, fileID := range diskFiles {

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

func (c *Cleanup) ForceCleanup() {
	c.performCleanup()
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

	activeSessions, err := c.redis.GetAllActiveSessions(ctx)
	if err != nil {
		return nil, err
	}

	pendingFiles, err := c.redis.GetAllPendingFiles(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total_files_db":  totalFiles,
		"active_files_db": activeFiles,
		"total_reports":   totalReports,
		"total_size_db":   totalSize,
		"total_size_disk": diskSize,
		"active_sessions": len(activeSessions),
		"pending_files":   len(pendingFiles),
	}, nil
}
