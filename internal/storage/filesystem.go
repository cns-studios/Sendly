package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sendly/internal/config"
)

// assemblyTempPrefix names a blob that is still being assembled.
const assemblyTempPrefix = ".assembling-"

type Filesystem struct {
	dataDir  string
	chunkDir string
	finalDir string
}

func NewFilesystem(cfg *config.Config) (*Filesystem, error) {
	dataDir := cfg.DataDir

	chunkDir := cfg.ChunkDir
	if chunkDir == "" {
		chunkDir = filepath.Join(dataDir, "chunks")
	}

	finalDir := filepath.Join(dataDir, "files")

	dirs := []string{dataDir, chunkDir, finalDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return &Filesystem{
		dataDir:  dataDir,
		chunkDir: chunkDir,
		finalDir: finalDir,
	}, nil
}

func (fs *Filesystem) GetChunkDir(sessionID string) string {
	return filepath.Join(fs.chunkDir, sessionID)
}

func (fs *Filesystem) CreateChunkDir(sessionID string) error {
	dir := fs.GetChunkDir(sessionID)
	return os.MkdirAll(dir, 0755)
}

func (fs *Filesystem) SaveChunk(sessionID string, chunkIndex int, data io.Reader) (int64, error) {
	dir := fs.GetChunkDir(sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}

	// Written under a temporary name and renamed into place: a replica killed
	// mid-write leaves no partial chunk_N behind, and a retried chunk simply
	// replaces the earlier copy.
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".chunk_%d.tmp-*", chunkIndex))
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(tmp, data)
	if err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), fs.GetChunkPath(sessionID, chunkIndex))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return written, nil
}

func (fs *Filesystem) GetChunkPath(sessionID string, chunkIndex int) string {
	return filepath.Join(fs.GetChunkDir(sessionID), fmt.Sprintf("chunk_%d", chunkIndex))
}

func (fs *Filesystem) ChunkExists(sessionID string, chunkIndex int) bool {
	path := fs.GetChunkPath(sessionID, chunkIndex)
	_, err := os.Stat(path)
	return err == nil
}

func (fs *Filesystem) GetChunkCount(sessionID string) (int, error) {
	dir := fs.GetChunkDir(sessionID)

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "chunk_") {
			count++
		}
	}
	return count, nil
}

func (fs *Filesystem) DeleteChunks(sessionID string) error {
	dir := fs.GetChunkDir(sessionID)
	return os.RemoveAll(dir)
}

// AssembleChunks joins the chunks of a session into the file's blob. The blob
// is built under a temporary name and renamed into place, so it either exists
// complete or not at all. The chunks are left in place: the caller deletes
// them once the database records the blob, so an assembly interrupted at any
// point can be run again from the start by any replica.
func (fs *Filesystem) AssembleChunks(ctx context.Context, sessionID, fileID string, totalChunks int) error {
	chunkDir := fs.GetChunkDir(sessionID)

	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		return fmt.Errorf("failed to read chunk directory: %w", err)
	}
	present := make(map[int]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "chunk_") {
			continue
		}
		if index, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "chunk_")); err == nil {
			present[index] = true
		}
	}
	for i := 0; i < totalChunks; i++ {
		if !present[i] {
			return fmt.Errorf("chunk %d of %d is missing", i, totalChunks)
		}
	}

	tmp, err := os.CreateTemp(fs.finalDir, assemblyTempPrefix+fileID+"-*")
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}

	for i := 0; i < totalChunks; i++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		chunkFile, err := os.Open(fs.GetChunkPath(sessionID, i))
		if err != nil {
			return fail(fmt.Errorf("failed to open chunk %d: %w", i, err))
		}
		_, err = io.Copy(tmp, chunkFile)
		chunkFile.Close()
		if err != nil {
			return fail(fmt.Errorf("failed to copy chunk %d: %w", i, err))
		}
	}

	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("failed to sync final file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to close final file: %w", err)
	}
	if err := os.Rename(tmp.Name(), fs.GetFilePath(fileID)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to move final file into place: %w", err)
	}
	return nil
}

func (fs *Filesystem) GetFilePath(fileID string) string {
	return filepath.Join(fs.finalDir, fileID)
}

func (fs *Filesystem) FileExists(fileID string) bool {
	path := fs.GetFilePath(fileID)
	_, err := os.Stat(path)
	return err == nil
}

func (fs *Filesystem) GetFileSize(fileID string) (int64, error) {
	path := fs.GetFilePath(fileID)
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (fs *Filesystem) OpenFile(fileID string) (*os.File, error) {
	path := fs.GetFilePath(fileID)
	return os.Open(path)
}

func (fs *Filesystem) DeleteFile(fileID string) error {
	path := fs.GetFilePath(fileID)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (fs *Filesystem) GetFileReader(fileID string) (io.ReadCloser, error) {
	return fs.OpenFile(fileID)
}

func (fs *Filesystem) GetAllFileIDs() ([]string, error) {
	entries, err := os.ReadDir(fs.finalDir)
	if err != nil {
		return nil, err
	}

	fileIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		// Dotfiles are assemblies in progress and health probes, not blobs.
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			fileIDs = append(fileIDs, entry.Name())
		}
	}
	return fileIDs, nil
}

func (fs *Filesystem) GetAllSessionIDs() ([]string, error) {
	entries, err := os.ReadDir(fs.chunkDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	sessionIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			sessionIDs = append(sessionIDs, entry.Name())
		}
	}
	return sessionIDs, nil
}

func (fs *Filesystem) GetTotalSize() (int64, error) {
	var totalSize int64

	err := filepath.Walk(fs.finalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return totalSize, err
}

// CleanupOrphanedChunks deletes chunk directories of sessions that are not in
// activeSessions and were last touched more than minAge ago. The age guard
// covers a session created on another replica after activeSessions was read.
func (fs *Filesystem) CleanupOrphanedChunks(activeSessions map[string]bool, minAge time.Duration) (int, error) {
	sessionIDs, err := fs.GetAllSessionIDs()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, sessionID := range sessionIDs {
		if activeSessions[sessionID] {
			continue
		}
		info, err := os.Stat(fs.GetChunkDir(sessionID))
		if err != nil || time.Since(info.ModTime()) < minAge {
			continue
		}
		if err := fs.DeleteChunks(sessionID); err != nil {
			fmt.Printf("Warning: failed to delete orphaned chunks for session %s: %v\n", sessionID, err)
			continue
		}
		cleaned++
	}

	return cleaned, nil
}

// CleanupStaleAssemblies deletes partial blobs left by assemblies that were
// interrupted more than minAge ago.
func (fs *Filesystem) CleanupStaleAssemblies(minAge time.Duration) (int, error) {
	entries, err := os.ReadDir(fs.finalDir)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), assemblyTempPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < minAge {
			continue
		}
		if err := os.Remove(filepath.Join(fs.finalDir, entry.Name())); err == nil {
			cleaned++
		}
	}
	return cleaned, nil
}
