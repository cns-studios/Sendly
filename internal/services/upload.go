package services

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"shareit/internal/config"
	"shareit/internal/models"
	"shareit/internal/storage"
)

type Upload struct {
	cfg      *config.Config
	db       *storage.Postgres
	redis    *storage.Redis
	fs       *storage.Filesystem
	stopChan chan struct{}
	wg       sync.WaitGroup
}

type FinalizeUploadOptions struct {
	OwnerCNSUserID   *int64
	OwnerCNSUserName *string
	TunnelID         string
	TunnelExpiresAt  time.Time
	WrappedDEK       []byte
	DEKWrapAlg       string
	DEKWrapNonce     []byte
	DEKWrapVersion   int
}

func NewUpload(cfg *config.Config, db *storage.Postgres, redis *storage.Redis, fs *storage.Filesystem) *Upload {
	return &Upload{
		cfg:      cfg,
		db:       db,
		redis:    redis,
		fs:       fs,
		stopChan: make(chan struct{}),
	}
}

func (u *Upload) StartPendingCleanup() {
	u.wg.Add(1)
	go u.runPendingCleanup()
}

func (u *Upload) Stop() {
	close(u.stopChan)
	u.wg.Wait()
}

func (u *Upload) runPendingCleanup() {
	defer u.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			u.cleanupPendingUploads()
		case <-u.stopChan:
			log.Println("Upload service stopping...")
			return
		}
	}
}

func (u *Upload) cleanupPendingUploads() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionIDs, err := u.fs.GetAllSessionIDs()
	if err != nil {
		log.Printf("Error getting session IDs: %v", err)
		return
	}

	for _, sessionID := range sessionIDs {

		session, err := u.redis.GetUploadSession(ctx, sessionID)
		if err == models.ErrSessionNotFound {

			log.Printf("Cleaning up expired session: %s", sessionID)
			if err := u.fs.DeleteChunks(sessionID); err != nil {
				log.Printf("Error deleting chunks for session %s: %v", sessionID, err)
			}
			continue
		}
		if err != nil {
			log.Printf("Error getting session %s: %v", sessionID, err)
			continue
		}

		isPending, err := u.redis.IsFilePending(ctx, session.FileID)
		if err != nil {
			log.Printf("Error checking pending status for file %s: %v", session.FileID, err)
			continue
		}

		if !isPending {

			chunkCount, chunkErr := u.fs.GetChunkCount(sessionID)
			if chunkErr != nil {
				log.Printf("Error checking chunk count for session %s: %v", sessionID, chunkErr)
				continue
			}

			if chunkCount == 0 && u.fs.FileExists(session.FileID) {
				log.Printf("Cleaning up abandoned pending upload: session=%s, file=%s", sessionID, session.FileID)
				u.CleanupSession(ctx, sessionID)
			}
		}
	}
}

func (u *Upload) InitUpload(ctx context.Context, req *models.UploadInitRequest, uploaderIP string) (*models.UploadInitResponse, error) {

	if req.FileSize > u.cfg.MaxFileSize {
		return nil, models.ErrFileTooLarge
	}

	sessionID := models.GenerateSessionID()
	fileID := models.GenerateID(17)

	session := &models.UploadSession{
		SessionID:    sessionID,
		FileID:       fileID,
		OriginalName: req.FileName,
		TotalSize:    req.FileSize,
		TotalChunks:  req.TotalChunks,
		ChunkSize:    req.ChunkSize,
		UploaderIP:   uploaderIP,
		CreatedAt:    time.Now(),
	}

	if err := u.redis.CreateUploadSession(ctx, session); err != nil {
		log.Printf("ERROR CreateUploadSession: %v", err)
		return nil, fmt.Errorf("error creating upload session: %w", err)
	}

	if err := u.fs.CreateChunkDir(sessionID); err != nil {
		log.Printf("ERROR CreateChunkDir: %v", err)
		u.redis.DeleteUploadSession(ctx, sessionID)
		return nil, fmt.Errorf("error creating chunk directory: %w", err)
	}

	return &models.UploadInitResponse{
		SessionID:   sessionID,
		FileID:      fileID,
		ChunkSize:   req.ChunkSize,
		TotalChunks: req.TotalChunks,
	}, nil
}

func (u *Upload) UploadChunk(ctx context.Context, sessionID string, chunkIndex int, data io.Reader) error {

	session, err := u.redis.GetUploadSession(ctx, sessionID)
	if err != nil {
		return err
	}

	if chunkIndex < 0 || chunkIndex >= session.TotalChunks {
		return models.ErrInvalidChunk
	}

	exists, err := u.redis.IsChunkUploaded(ctx, sessionID, chunkIndex)
	if err != nil {
		return err
	}
	if exists {
		return models.ErrChunkAlreadyExists
	}

	_, err = u.fs.SaveChunk(sessionID, chunkIndex, data)
	if err != nil {
		return fmt.Errorf("error saving chunk: %w", err)
	}

	if err := u.redis.MarkChunkUploaded(ctx, sessionID, chunkIndex); err != nil {

		return fmt.Errorf("error marking chunk as uploaded: %w", err)
	}

	if err := u.redis.ExtendUploadSession(ctx, sessionID); err != nil {
		log.Printf("Warning: failed to extend session TTL: %v", err)
	}

	return nil
}

func (u *Upload) CompleteUpload(ctx context.Context, sessionID string) (*models.UploadCompleteResponse, error) {

	session, err := u.redis.GetUploadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	uploadedCount, err := u.redis.GetUploadedChunkCount(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if int(uploadedCount) != session.TotalChunks {
		return nil, models.ErrUploadIncomplete
	}

	pendingTTL := u.redis.PendingTTL()
	if err := u.redis.MarkFilePending(ctx, session.FileID, sessionID); err != nil {
		return nil, fmt.Errorf("error marking file as pending: %w", err)
	}
	if err := u.redis.SetUploadSessionTTL(ctx, sessionID, pendingTTL); err != nil {
		log.Printf("Warning: failed to shrink session TTL for pending upload %s: %v", sessionID, err)
	}

	if err := u.redis.SetAssemblyStatus(ctx, sessionID, "pending"); err != nil {
		return nil, fmt.Errorf("error setting assembly status: %w", err)
	}

	go func() {
		bgCtx := context.Background()
		if err := u.fs.AssembleChunks(sessionID, session.FileID, session.TotalChunks); err != nil {
			log.Printf("Error assembling chunks for session %s: %v", sessionID, err)
			u.redis.SetAssemblyStatus(bgCtx, sessionID, "error:"+err.Error())
			u.redis.RemovePendingFile(bgCtx, session.FileID)
			return
		}
		u.redis.DeleteChunkTracking(bgCtx, sessionID)
		u.redis.SetAssemblyStatus(bgCtx, sessionID, "done")
		log.Printf("Assembly complete for session %s file %s", sessionID, session.FileID)
	}()

	return &models.UploadCompleteResponse{
		SessionID:        sessionID,
		FileID:           session.FileID,
		PendingExpiresAt: time.Now().Add(pendingTTL),
	}, nil
}

func (u *Upload) GetAssemblyStatus(ctx context.Context, sessionID string) (string, error) {
	return u.redis.GetAssemblyStatus(ctx, sessionID)
}

func (u *Upload) FinalizeUpload(ctx context.Context, sessionID, duration string) (*models.UploadFinalizeResponse, error) {
	return u.FinalizeUploadWithOptions(ctx, sessionID, duration, nil)
}

func (u *Upload) FinalizeUploadWithOptions(ctx context.Context, sessionID, duration string, opts *FinalizeUploadOptions) (*models.UploadFinalizeResponse, error) {
	session, err := u.redis.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == models.ErrSessionNotFound {
			return nil, models.ErrSessionExpired
		}
		return nil, err
	}
	var isPending bool
	isPending, err = u.redis.IsFilePending(ctx, session.FileID)
	if err != nil {
		return nil, err
	}
	if !isPending {
		return nil, models.ErrUploadNotPending
	}

	var dur time.Duration
	var expiresAt time.Time
	if opts != nil && opts.TunnelID != "" {
		if opts.TunnelExpiresAt.IsZero() {
			return nil, fmt.Errorf("error resolving tunnel expiry")
		}
		expiresAt = opts.TunnelExpiresAt
		if time.Until(expiresAt) <= 0 {
			return nil, models.ErrFileExpired
		}
	} else {
		dur, err = models.ParseFinalizeDuration(duration)
		if err != nil {
			return nil, err
		}
		expiresAt = time.Now().Add(dur)
	}

	actualSize, err := u.fs.GetFileSize(session.FileID)
	if err != nil {
		return nil, fmt.Errorf("error getting file size: %w", err)
	}

	numericCode, err := u.generateUniqueNumericCode(ctx)
	if err != nil {
		return nil, err
	}

	file := &models.File{
		ID:           session.FileID,
		NumericCode:  numericCode,
		OriginalName: session.OriginalName,
		SizeBytes:    actualSize,
		UploaderIP:   session.UploaderIP,
		ExpiresAt:    expiresAt,
		CreatedAt:    time.Now(),
	}

	if opts != nil {
		if opts.OwnerCNSUserID != nil {
			file.OwnerCNSUserID = sql.NullInt64{Int64: *opts.OwnerCNSUserID, Valid: true}
		}
		if opts.OwnerCNSUserName != nil {
			file.OwnerCNSUserName = sql.NullString{String: *opts.OwnerCNSUserName, Valid: true}
		}
		if opts.TunnelID != "" {
			file.TunnelID = sql.NullString{String: opts.TunnelID, Valid: true}
		}
	}

	var envelope *models.FileKeyEnvelope
	if opts != nil && len(opts.WrappedDEK) > 0 {
		wrapVersion := opts.DEKWrapVersion
		if wrapVersion <= 0 {
			wrapVersion = 1
		}
		envelope = &models.FileKeyEnvelope{
			FileID:         session.FileID,
			WrappedDEK:     opts.WrappedDEK,
			DEKWrapAlg:     opts.DEKWrapAlg,
			DEKWrapNonce:   opts.DEKWrapNonce,
			DEKWrapVersion: wrapVersion,
		}
	}

	if err := u.db.CreateFileWithEnvelope(ctx, file, envelope); err != nil {
		return nil, fmt.Errorf("error creating file record: %w", err)
	}

	u.redis.RemovePendingFile(ctx, session.FileID)
	u.redis.DeleteUploadSession(ctx, sessionID)
	u.redis.DeleteChunkTracking(ctx, sessionID)

	shareURL := fmt.Sprintf("%s/shared/%s", u.cfg.BaseURL, session.FileID)
	return &models.UploadFinalizeResponse{
		FileID:      session.FileID,
		NumericCode: numericCode,
		ShareURL:    shareURL,
	}, nil
}

func (u *Upload) generateUniqueNumericCode(ctx context.Context) (string, error) {
	for i := 0; i < 10; i++ {
		numericCode := models.GenerateNumericCode()
		exists, err := u.db.NumericCodeExists(ctx, numericCode)
		if err != nil {
			return "", fmt.Errorf("error checking numeric code: %w", err)
		}
		if !exists {
			return numericCode, nil
		}
	}

	return "", fmt.Errorf("failed to generate unique numeric code")
}

func (u *Upload) CancelUpload(ctx context.Context, sessionID string) error {

	session, err := u.redis.GetUploadSession(ctx, sessionID)
	if err != nil && err != models.ErrSessionNotFound {
		return err
	}

	if err := u.fs.DeleteChunks(sessionID); err != nil {
		log.Printf("Error deleting chunks for session %s: %v", sessionID, err)
	}

	if session != nil {
		if u.fs.FileExists(session.FileID) {
			u.fs.DeleteFile(session.FileID)
		}
		u.redis.RemovePendingFile(ctx, session.FileID)
	}
	u.redis.DeleteUploadSession(ctx, sessionID)
	u.redis.DeleteChunkTracking(ctx, sessionID)

	return nil
}

func (u *Upload) CleanupSession(ctx context.Context, sessionID string) {
	session, _ := u.redis.GetUploadSession(ctx, sessionID)

	if err := u.fs.DeleteChunks(sessionID); err != nil {
		log.Printf("Error deleting chunks for session %s: %v", sessionID, err)
	}

	if session != nil {
		if u.fs.FileExists(session.FileID) {
			_, err := u.db.GetFileByID(ctx, session.FileID)
			if err == models.ErrFileNotFound {

				u.fs.DeleteFile(session.FileID)
			}
		}
		u.redis.RemovePendingFile(ctx, session.FileID)
	}

	u.redis.DeleteUploadSession(ctx, sessionID)
	u.redis.DeleteChunkTracking(ctx, sessionID)
}

func (u *Upload) GetUploadProgress(ctx context.Context, sessionID string) (int, int, error) {
	session, err := u.redis.GetUploadSession(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}

	uploadedCount, err := u.redis.GetUploadedChunkCount(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}

	return int(uploadedCount), session.TotalChunks, nil
}
