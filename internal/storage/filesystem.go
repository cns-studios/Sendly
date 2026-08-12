package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sendly/internal/config"
)

type Filesystem struct {
	dataDir   string
	chunkDir  string
	finalDir  string
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

	chunkPath := filepath.Join(dir, fmt.Sprintf("chunk_%d", chunkIndex))
	
	file, err := os.Create(chunkPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	written, err := io.Copy(file, data)
	if err != nil {
		 
		os.Remove(chunkPath)
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

 

 
func (fs *Filesystem) AssembleChunks(sessionID, fileID string, totalChunks int) error {
	chunkDir := fs.GetChunkDir(sessionID)
	finalPath := fs.GetFilePath(fileID)

	 
	finalFile, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	defer finalFile.Close()

	 
	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		return fmt.Errorf("failed to read chunk directory: %w", err)
	}

	 
	type chunkInfo struct {
		index int
		path  string
	}
	chunks := make([]chunkInfo, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "chunk_") {
			continue
		}

		indexStr := strings.TrimPrefix(entry.Name(), "chunk_")
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			continue
		}

		chunks = append(chunks, chunkInfo{
			index: index,
			path:  filepath.Join(chunkDir, entry.Name()),
		})
	}

	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].index < chunks[j].index
	})

	 
	if len(chunks) != totalChunks {
		return fmt.Errorf("expected %d chunks, found %d", totalChunks, len(chunks))
	}

	 
	for _, chunk := range chunks {
		chunkFile, err := os.Open(chunk.path)
		if err != nil {
			 
			finalFile.Close()
			os.Remove(finalPath)
			return fmt.Errorf("failed to open chunk %d: %w", chunk.index, err)
		}

		_, err = io.Copy(finalFile, chunkFile)
		chunkFile.Close()

		if err != nil {
			finalFile.Close()
			os.Remove(finalPath)
			return fmt.Errorf("failed to copy chunk %d: %w", chunk.index, err)
		}
	}

	 
	if err := fs.DeleteChunks(sessionID); err != nil {
		 
		fmt.Printf("Warning: failed to delete chunks for session %s: %v\n", sessionID, err)
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
		if !entry.IsDir() {
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

 
func (fs *Filesystem) CleanupOrphanedChunks(activeSessions map[string]bool) (int, error) {
	sessionIDs, err := fs.GetAllSessionIDs()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, sessionID := range sessionIDs {
		if !activeSessions[sessionID] {
			if err := fs.DeleteChunks(sessionID); err != nil {
				fmt.Printf("Warning: failed to delete orphaned chunks for session %s: %v\n", sessionID, err)
				continue
			}
			cleaned++
		}
	}

	return cleaned, nil
}