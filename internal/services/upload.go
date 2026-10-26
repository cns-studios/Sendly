package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

const (
	// uploadSessionTTL is how long an upload may sit idle between chunks.
	uploadSessionTTL = 1 * time.Hour
	// uploadPendingTTL is how long an upload waits for finalize after complete.
	uploadPendingTTL = 10 * time.Minute

	// A replica assembling an upload holds a lease on it and renews it
	// regularly. If the replica dies, the lease runs out and another replica
	// resumes the assembly from the chunks, which are kept until it is done.
	assemblyLease       = 30 * time.Second
	assemblyRenewEvery  = 10 * time.Second
	assemblyResumeEvery = 10 * time.Second
)

type Upload struct {
	cfg     *config.Config
	db      *storage.Postgres
	fs      *storage.Filesystem
	tracker *Tracker
	// owner identifies this replica in assembly leases.
	owner string

	stopChan chan struct{}
	wg       sync.WaitGroup

	// Assemblies run under assemblyCtx so a shutdown can interrupt them.
	assemblyCtx    context.Context
	stopAssemblies context.CancelFunc
	assemblies     sync.WaitGroup
	// running holds the sessions this replica is assembling right now. The
	// lease alone can't tell: a restarted pod keeps its name, so the lease
	// owner matches this replica either way.
	runningMu sync.Mutex
	running   map[string]bool
}

type FinalizeUploadOptions struct {
	OwnerCNSUserID         *int64
	OwnerCNSUserName       *string
	TunnelID               string
	TunnelExpiresAt        time.Time
	WrappedDEK             []byte
	DEKWrapAlg             string
	DEKWrapNonce           []byte
	DEKWrapVersion         int
	IdentityWrappedDEK     []byte
	IdentityDEKWrapAlg     string
	IdentityDEKWrapNonce   []byte
	IdentityDEKWrapVersion int
	IdentityKeyVersion     int
}

func NewUpload(cfg *config.Config, db *storage.Postgres, fs *storage.Filesystem, tracker *Tracker) *Upload {
	ctx, cancel := context.WithCancel(context.Background())
	return &Upload{
		cfg:            cfg,
		db:             db,
		fs:             fs,
		tracker:        tracker,
		owner:          cfg.InstanceName,
		stopChan:       make(chan struct{}),
		assemblyCtx:    ctx,
		stopAssemblies: cancel,
		running:        make(map[string]bool),
	}
}

// Start runs the background loops: resuming assemblies that lost their
// replica, and removing expired upload sessions. Every replica runs both;
// leases and row locks keep them from doing the same work twice.
func (u *Upload) Start() {
	u.wg.Add(2)
	go u.runLoop(assemblyResumeEvery, u.resumeOrphanedAssemblies)
	go u.runLoop(time.Minute, u.cleanupExpiredSessions)
}

// Stop ends the background loops and gives running assemblies until drain
// to finish. Whatever is still running then is interrupted and its lease
// released, so another replica picks it up at once.
func (u *Upload) Stop(drain time.Duration) {
	close(u.stopChan)
	u.wg.Wait()

	done := make(chan struct{})
	go func() { u.assemblies.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drain):
		u.runningMu.Lock()
		unfinished := len(u.running)
		u.runningMu.Unlock()
		if unfinished > 0 {
			log.Printf("Upload service: handing %d unfinished assemblies over to other replicas", unfinished)
		}
		u.stopAssemblies()
		<-done
	}
	u.stopAssemblies()
}

func (u *Upload) runLoop(every time.Duration, fn func()) {
	defer u.wg.Done()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fn()
		case <-u.stopChan:
			return
		}
	}
}

func (u *Upload) resumeOrphanedAssemblies() {
	ctx, cancel := context.WithTimeout(u.assemblyCtx, 10*time.Second)
	defer cancel()
	ids, err := u.db.ListOrphanedAssemblies(ctx, 10)
	if err != nil {
		log.Printf("Upload service: listing orphaned assemblies failed: %v", err)
		return
	}
	for _, id := range ids {
		u.startAssembly(id)
	}
}

func (u *Upload) cleanupExpiredSessions() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for {
		sessions, err := u.db.TakeExpiredUploadSessions(ctx, 100)
		if err != nil {
			log.Printf("Upload service: taking expired sessions failed: %v", err)
			return
		}
		for _, s := range sessions {
			u.removeSessionFiles(ctx, &s)
		}
		if len(sessions) < 100 {
			return
		}
	}
}

// removeSessionFiles deletes a dropped session's chunks and, unless it was
// finalized into a file, its blob.
func (u *Upload) removeSessionFiles(ctx context.Context, s *models.UploadSession) {
	if err := u.fs.DeleteChunks(s.SessionID); err != nil {
		log.Printf("Error deleting chunks for session %s: %v", s.SessionID, err)
	}
	if !u.fs.FileExists(s.FileID) {
		return
	}
	if _, err := u.db.GetFileForAdmin(ctx, s.FileID); errors.Is(err, models.ErrFileNotFound) {
		if err := u.fs.DeleteFile(s.FileID); err != nil {
			log.Printf("Error deleting unfinalized blob %s: %v", s.FileID, err)
		}
	}
}

func (u *Upload) InitUpload(ctx context.Context, req *models.UploadInitRequest, uploaderIP string, maxFileSize int64) (*models.UploadInitResponse, error) {

	if req.FileSize > maxFileSize {
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

	if err := u.db.CreateUploadSession(ctx, session, uploadSessionTTL); err != nil {
		log.Printf("ERROR CreateUploadSession: %v", err)
		return nil, fmt.Errorf("error creating upload session: %w", err)
	}

	if err := u.fs.CreateChunkDir(sessionID); err != nil {
		log.Printf("ERROR CreateChunkDir: %v", err)
		u.db.DeleteUploadSession(ctx, sessionID)
		return nil, fmt.Errorf("error creating chunk directory: %w", err)
	}

	return &models.UploadInitResponse{
		SessionID:   sessionID,
		FileID:      fileID,
		ChunkSize:   req.ChunkSize,
		TotalChunks: req.TotalChunks,
	}, nil
}

// UploadChunk stores one chunk. Sending a chunk again (a client retrying
// after a lost response, possibly to another replica) replaces it.
func (u *Upload) UploadChunk(ctx context.Context, sessionID string, chunkIndex int, data io.Reader) error {

	session, err := u.db.GetUploadSession(ctx, sessionID)
	if err != nil {
		return err
	}

	if chunkIndex < 0 || chunkIndex >= session.TotalChunks {
		return models.ErrInvalidChunk
	}
	if session.State != models.UploadStateUploading {
		return models.ErrUploadNotAccepting
	}

	if _, err := u.fs.SaveChunk(sessionID, chunkIndex, data); err != nil {
		return fmt.Errorf("error saving chunk: %w", err)
	}

	if err := u.db.RecordChunk(ctx, sessionID, chunkIndex, uploadSessionTTL); err != nil {
		if errors.Is(err, models.ErrUploadNotAccepting) {
			return err
		}
		return fmt.Errorf("error marking chunk as uploaded: %w", err)
	}

	return nil
}

// CompleteUpload starts assembling the blob in the background; the client
// polls GetAssemblyStatus. Repeating it is harmless and returns the same
// answer, so a client may retry it after a lost response.
func (u *Upload) CompleteUpload(ctx context.Context, sessionID string) (*models.UploadCompleteResponse, error) {

	session, err := u.db.StartAssembly(ctx, sessionID, uploadPendingTTL)
	if err != nil {
		return nil, err
	}

	if session.State == models.UploadStateAssembling {
		u.startAssembly(sessionID)
	}

	return &models.UploadCompleteResponse{
		SessionID:        sessionID,
		FileID:           session.FileID,
		PendingExpiresAt: session.ExpiresAt,
	}, nil
}

// startAssembly assembles the session in the background if this replica can
// take its lease. It returns at once; without the lease it does nothing.
func (u *Upload) startAssembly(sessionID string) {
	u.runningMu.Lock()
	if u.running[sessionID] {
		u.runningMu.Unlock()
		return
	}
	u.running[sessionID] = true
	u.runningMu.Unlock()
	done := func() {
		u.runningMu.Lock()
		delete(u.running, sessionID)
		u.runningMu.Unlock()
	}

	session, err := u.db.ClaimAssembly(u.assemblyCtx, sessionID, u.owner, assemblyLease)
	if err != nil || session == nil {
		if err != nil {
			log.Printf("Claiming assembly of session %s failed: %v", sessionID, err)
		}
		done()
		return
	}

	u.assemblies.Add(1)
	go func() {
		defer u.assemblies.Done()
		defer done()
		u.assemble(session)
	}()
}

func (u *Upload) assemble(session *models.UploadSession) {
	ctx, cancel := context.WithCancel(u.assemblyCtx)
	defer cancel()

	// Keep the lease while working; losing it means another replica has
	// taken over, so stop.
	go func() {
		ticker := time.NewTicker(assemblyRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := u.db.RenewAssembly(ctx, session.SessionID, u.owner, assemblyLease)
				if err != nil {
					// The database may be failing over: keep going, the fenced
					// update at the end decides whether this run counts.
					log.Printf("Renewing assembly lease of session %s failed: %v", session.SessionID, err)
					continue
				}
				if !ok {
					log.Printf("Assembly lease of session %s was lost", session.SessionID)
					cancel()
					return
				}
			}
		}
	}()

	err := u.fs.AssembleChunks(ctx, session.SessionID, session.FileID, session.TotalChunks)

	// The outcome is recorded even while shutting down, so it must not use
	// the cancelled context; retry it so a database failover is waited out.
	record := func(what string, fn func(context.Context) error) {
		deadline := time.Now().Add(2 * time.Minute)
		for {
			recordCtx, recordCancel := context.WithTimeout(context.Background(), 10*time.Second)
			recErr := fn(recordCtx)
			recordCancel()
			if recErr == nil || time.Now().After(deadline) {
				if recErr != nil {
					log.Printf("Recording %s for session %s failed: %v", what, session.SessionID, recErr)
				}
				return
			}
			time.Sleep(2 * time.Second)
		}
	}

	switch {
	case err == nil:
		var finished bool
		record("assembly", func(ctx context.Context) (err error) {
			finished, err = u.db.FinishAssembly(ctx, session.SessionID, u.owner, uploadPendingTTL/2)
			return err
		})
		if finished {
			if err := u.fs.DeleteChunks(session.SessionID); err != nil {
				log.Printf("Warning: failed to delete chunks for session %s: %v", session.SessionID, err)
			}
			log.Printf("Assembly complete for session %s file %s", session.SessionID, session.FileID)
		}
	case ctx.Err() != nil:
		// Interrupted by shutdown or a lost lease: let another replica redo it.
		record("lease release", func(ctx context.Context) error {
			return u.db.ReleaseAssembly(ctx, session.SessionID, u.owner)
		})
	default:
		log.Printf("Error assembling chunks for session %s: %v", session.SessionID, err)
		record("assembly failure", func(ctx context.Context) error {
			return u.db.FailAssembly(ctx, session.SessionID, u.owner, err.Error())
		})
	}
}

// GetAssemblyStatus answers "pending" while the blob is being assembled,
// "done" once it can be finalized, or "error:<message>".
func (u *Upload) GetAssemblyStatus(ctx context.Context, sessionID string) (string, error) {
	session, err := u.db.GetUploadSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	switch session.State {
	case models.UploadStateAssembled, models.UploadStateFinalized:
		return "done", nil
	case models.UploadStateFailed:
		return "error:" + session.Error.String, nil
	case models.UploadStateAssembling:
		return "pending", nil
	default:
		return "", models.ErrSessionNotFound
	}
}

func (u *Upload) FinalizeUpload(ctx context.Context, sessionID, duration string) (*models.UploadFinalizeResponse, error) {
	return u.FinalizeUploadWithOptions(ctx, sessionID, duration, nil)
}

func (u *Upload) FinalizeUploadWithOptions(ctx context.Context, sessionID, duration string, opts *FinalizeUploadOptions) (*models.UploadFinalizeResponse, error) {
	session, err := u.db.GetUploadSession(ctx, sessionID)
	if err != nil {
		if err == models.ErrSessionNotFound {
			return nil, models.ErrSessionExpired
		}
		return nil, err
	}

	// A finalize repeated after its response was lost answers with the file
	// the first one created.
	if existing, err := u.db.GetFileForAdmin(ctx, session.FileID); err == nil {
		if session.State != models.UploadStateFinalized {
			u.db.MarkUploadFinalized(ctx, sessionID)
		}
		return u.finalizeResponse(existing), nil
	} else if !errors.Is(err, models.ErrFileNotFound) {
		return nil, err
	}

	switch session.State {
	case models.UploadStateAssembled:
	case models.UploadStateAssembling:
		return nil, models.ErrAssemblyInProgress
	default:
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

	// Guests' quick share uploads keep a key wrapped for their throwaway
	// participant key; signed-in uploaders keep only their identity-wrapped one.
	var envelope *models.FileKeyEnvelope
	if opts != nil && opts.OwnerCNSUserID == nil && len(opts.WrappedDEK) > 0 {
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

	var identityEnvelope *models.FileAccessKeyEnvelope
	if opts != nil && opts.OwnerCNSUserID != nil {
		if len(opts.IdentityWrappedDEK) == 0 {
			return nil, models.ErrIdentityEnvelopeRequired
		}
		activeKey, err := u.db.GetActiveUserIdentityKey(ctx, *opts.OwnerCNSUserID)
		if errors.Is(err, models.ErrIdentityKeyNotFound) {
			return nil, models.ErrIdentityEnvelopeRequired
		}
		if err != nil {
			return nil, fmt.Errorf("owner identity key unavailable: %w", err)
		}
		keyVersion := activeKey.KeyVersion
		if opts.IdentityKeyVersion != keyVersion {
			return nil, models.ErrIdentityKeyStale
		}
		identityEnvelope = &models.FileAccessKeyEnvelope{
			FileID: session.FileID, RecipientCNSUserID: *opts.OwnerCNSUserID,
			WrappedDEK: opts.IdentityWrappedDEK, DEKWrapAlg: opts.IdentityDEKWrapAlg,
			DEKWrapNonce: opts.IdentityDEKWrapNonce, DEKWrapVersion: opts.IdentityDEKWrapVersion,
			RecipientKeyVersion: keyVersion, AccessKind: "owner",
			GrantedAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if identityEnvelope.DEKWrapVersion <= 0 {
			identityEnvelope.DEKWrapVersion = 1
		}
	}

	if err := u.db.CreateFileWithEnvelope(ctx, file, envelope, identityEnvelope); err != nil {
		// Two finalize requests of the same upload raced; the first one won.
		if existing, getErr := u.db.GetFileForAdmin(ctx, session.FileID); getErr == nil {
			return u.finalizeResponse(existing), nil
		}
		return nil, fmt.Errorf("error creating file record: %w", err)
	}

	u.tracker.RecordUpload(ctx, actualSize, session.UploaderIP)

	// The session stays until it expires so that a repeated finalize can be
	// answered; cleanup removes it then.
	if err := u.db.MarkUploadFinalized(ctx, sessionID); err != nil {
		log.Printf("Warning: failed to mark session %s finalized: %v", sessionID, err)
	}

	return u.finalizeResponse(file), nil
}

func (u *Upload) finalizeResponse(file *models.File) *models.UploadFinalizeResponse {
	return &models.UploadFinalizeResponse{
		FileID:      file.ID,
		NumericCode: file.NumericCode,
		ShareURL:    fmt.Sprintf("%s/shared/%s", u.cfg.BaseURL, file.ID),
	}
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

	session, err := u.db.GetUploadSession(ctx, sessionID)
	if err == models.ErrSessionNotFound {
		return u.fs.DeleteChunks(sessionID)
	}
	if err != nil {
		return err
	}
	if session.State == models.UploadStateFinalized {
		return nil
	}

	if err := u.db.DeleteUploadSession(ctx, sessionID); err != nil {
		return err
	}
	u.removeSessionFiles(ctx, session)
	return nil
}

func (u *Upload) GetUploadProgress(ctx context.Context, sessionID string) (int, int, error) {
	session, err := u.db.GetUploadSession(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}

	uploadedCount, err := u.db.CountUploadedChunks(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}

	return uploadedCount, session.TotalChunks, nil
}
