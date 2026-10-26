package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"sendly/internal/models"
)

// Upload sessions live in Postgres so that any replica can serve any request
// of an upload, and so that an upload survives the replica that started it.

const uploadSessionColumns = `session_id, file_id, original_name, total_size, total_chunks, chunk_size,
	uploader_ip, state, error, expires_at, created_at`

func (p *Postgres) CreateUploadSession(ctx context.Context, s *models.UploadSession, ttl time.Duration) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO upload_sessions (session_id, file_id, original_name, total_size, total_chunks, chunk_size,
			uploader_ip, state, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW() + make_interval(secs => $9), $10)`,
		s.SessionID, s.FileID, s.OriginalName, s.TotalSize, s.TotalChunks, s.ChunkSize,
		s.UploaderIP, models.UploadStateUploading, ttl.Seconds(), s.CreatedAt)
	return err
}

// GetUploadSession returns a live session. An expired one is reported as not
// found even before cleanup has removed it, except while a replica holds its
// assembly lease.
func (p *Postgres) GetUploadSession(ctx context.Context, sessionID string) (*models.UploadSession, error) {
	var s models.UploadSession
	err := p.db.GetContext(ctx, &s, `
		SELECT `+uploadSessionColumns+` FROM upload_sessions
		WHERE session_id = $1
		  AND (expires_at > NOW() OR (state = 'assembling' AND assembly_lease_until > NOW()))`, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// RecordChunk notes that chunkIndex is on disk and extends the session. It
// is idempotent: a chunk sent again after a lost response is accepted.
func (p *Postgres) RecordChunk(ctx context.Context, sessionID string, chunkIndex int, ttl time.Duration) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE upload_sessions SET expires_at = NOW() + make_interval(secs => $2), updated_at = NOW()
		WHERE session_id = $1 AND state = 'uploading' AND expires_at > NOW()`, sessionID, ttl.Seconds())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrUploadNotAccepting
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO upload_chunks (session_id, chunk_index) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, sessionID, chunkIndex); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) CountUploadedChunks(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := p.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM upload_chunks WHERE session_id = $1`, sessionID)
	return n, err
}

// StartAssembly moves a fully uploaded session to assembling and gives it
// pendingTTL to be finalized. Calling it again for a session that is already
// past that point returns the session unchanged, so a retried complete
// request gets the same answer. A failed session may be started again.
func (p *Postgres) StartAssembly(ctx context.Context, sessionID string, pendingTTL time.Duration) (*models.UploadSession, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var s models.UploadSession
	err = tx.GetContext(ctx, &s, `
		SELECT `+uploadSessionColumns+` FROM upload_sessions
		WHERE session_id = $1 AND expires_at > NOW()
		FOR UPDATE`, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.State != models.UploadStateUploading && s.State != models.UploadStateFailed {
		return &s, tx.Commit()
	}

	var uploaded int
	if err := tx.GetContext(ctx, &uploaded, `SELECT COUNT(*) FROM upload_chunks WHERE session_id = $1`, sessionID); err != nil {
		return nil, err
	}
	if uploaded != s.TotalChunks {
		return nil, models.ErrUploadIncomplete
	}

	if err := tx.GetContext(ctx, &s, `
		UPDATE upload_sessions
		SET state = 'assembling', error = NULL, assembly_owner = NULL, assembly_lease_until = NULL,
		    expires_at = NOW() + make_interval(secs => $2), updated_at = NOW()
		WHERE session_id = $1
		RETURNING `+uploadSessionColumns, sessionID, pendingTTL.Seconds()); err != nil {
		return nil, err
	}
	return &s, tx.Commit()
}

// ClaimAssembly takes the assembly lease of a session for owner. It succeeds
// when the session is assembling and nobody else holds an unexpired lease.
func (p *Postgres) ClaimAssembly(ctx context.Context, sessionID, owner string, lease time.Duration) (*models.UploadSession, error) {
	var s models.UploadSession
	err := p.db.GetContext(ctx, &s, `
		UPDATE upload_sessions
		SET assembly_owner = $2, assembly_lease_until = NOW() + make_interval(secs => $3), updated_at = NOW()
		WHERE session_id = $1 AND state = 'assembling'
		  AND (assembly_lease_until IS NULL OR assembly_lease_until < NOW() OR assembly_owner = $2)
		RETURNING `+uploadSessionColumns, sessionID, owner, lease.Seconds())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

// RenewAssembly extends owner's lease. False means the lease was lost.
func (p *Postgres) RenewAssembly(ctx context.Context, sessionID, owner string, lease time.Duration) (bool, error) {
	res, err := p.db.ExecContext(ctx, `
		UPDATE upload_sessions SET assembly_lease_until = NOW() + make_interval(secs => $3)
		WHERE session_id = $1 AND state = 'assembling' AND assembly_owner = $2`, sessionID, owner, lease.Seconds())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FinishAssembly marks the blob as complete, fenced on owner's lease, and
// leaves at least minPending for finalize. False means the lease was lost.
func (p *Postgres) FinishAssembly(ctx context.Context, sessionID, owner string, minPending time.Duration) (bool, error) {
	res, err := p.db.ExecContext(ctx, `
		UPDATE upload_sessions
		SET state = 'assembled', assembly_owner = NULL, assembly_lease_until = NULL,
		    expires_at = GREATEST(expires_at, NOW() + make_interval(secs => $3)), updated_at = NOW()
		WHERE session_id = $1 AND state = 'assembling' AND assembly_owner = $2`, sessionID, owner, minPending.Seconds())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (p *Postgres) FailAssembly(ctx context.Context, sessionID, owner, message string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE upload_sessions
		SET state = 'failed', error = $3, assembly_owner = NULL, assembly_lease_until = NULL, updated_at = NOW()
		WHERE session_id = $1 AND state = 'assembling' AND assembly_owner = $2`, sessionID, owner, message)
	return err
}

// ReleaseAssembly gives up owner's lease at once, so another replica can
// resume the assembly without waiting for the lease to run out.
func (p *Postgres) ReleaseAssembly(ctx context.Context, sessionID, owner string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE upload_sessions SET assembly_owner = NULL, assembly_lease_until = NULL
		WHERE session_id = $1 AND state = 'assembling' AND assembly_owner = $2`, sessionID, owner)
	return err
}

// ListOrphanedAssemblies returns sessions that should be assembling but
// whose lease has run out or was released: the replica doing it went away.
func (p *Postgres) ListOrphanedAssemblies(ctx context.Context, limit int) ([]string, error) {
	var ids []string
	err := p.db.SelectContext(ctx, &ids, `
		SELECT session_id FROM upload_sessions
		WHERE state = 'assembling' AND (assembly_lease_until IS NULL OR assembly_lease_until < NOW())
		ORDER BY updated_at
		LIMIT $1`, limit)
	return ids, err
}

func (p *Postgres) MarkUploadFinalized(ctx context.Context, sessionID string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE upload_sessions SET state = 'finalized', updated_at = NOW() WHERE session_id = $1`, sessionID)
	return err
}

func (p *Postgres) DeleteUploadSession(ctx context.Context, sessionID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM upload_sessions WHERE session_id = $1`, sessionID)
	return err
}

// TakeExpiredUploadSessions deletes up to limit expired sessions and returns
// them, so the caller can remove their chunks and unfinalized blobs. Rows
// locked by another replica's cleanup are skipped. If the caller dies before
// removing the files, the orphan sweep catches them.
func (p *Postgres) TakeExpiredUploadSessions(ctx context.Context, limit int) ([]models.UploadSession, error) {
	var sessions []models.UploadSession
	err := p.db.SelectContext(ctx, &sessions, `
		DELETE FROM upload_sessions WHERE session_id IN (
			SELECT session_id FROM upload_sessions
			WHERE expires_at < NOW()
			  AND NOT (state = 'assembling' AND assembly_lease_until > NOW())
			ORDER BY expires_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING `+uploadSessionColumns, limit)
	return sessions, err
}

// UploadSessionRefs returns the session and file IDs of every upload session,
// which the orphan sweep must leave alone.
func (p *Postgres) UploadSessionRefs(ctx context.Context) (sessionIDs, fileIDs map[string]bool, err error) {
	rows, err := p.db.QueryContext(ctx, `SELECT session_id, file_id FROM upload_sessions`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	sessionIDs, fileIDs = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var sessionID, fileID string
		if err := rows.Scan(&sessionID, &fileID); err != nil {
			return nil, nil, err
		}
		sessionIDs[sessionID], fileIDs[fileID] = true, true
	}
	return sessionIDs, fileIDs, rows.Err()
}

// CountUploadSessions returns live sessions and those waiting for finalize.
func (p *Postgres) CountUploadSessions(ctx context.Context) (active, pending int, err error) {
	err = p.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE state IN ('assembling', 'assembled'))
		FROM upload_sessions WHERE expires_at > NOW()`).Scan(&active, &pending)
	return active, pending, err
}
