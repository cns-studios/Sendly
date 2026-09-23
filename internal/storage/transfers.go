package storage

import (
	"context"
	"database/sql"
	"errors"

	"sendly/internal/models"
)

// CreateTransfer records a pending transfer together with the recipient's
// key envelope (the DEK wrapped client-side for the recipient's identity
// key). Both rows are written atomically; a file can only be transferred to
// a given recipient once, so an existing (file, recipient) pair returns
// ErrTransferExists, even if that earlier transfer was declined.
func (p *Postgres) CreateTransfer(ctx context.Context, transfer *models.Transfer, envelope *models.FileAccessKeyEnvelope) error {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO file_transfers (file_id, sender_cns_user_id, recipient_cns_user_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (file_id, recipient_cns_user_id) DO NOTHING
	`, transfer.FileID, transfer.SenderCNSUserID, transfer.RecipientCNSUserID, models.TransferStatusPending, transfer.CreatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrTransferExists
	}

	res, err = tx.ExecContext(ctx, `
		INSERT INTO file_access_key_envelopes
			(file_id, recipient_cns_user_id, wrapped_dek, dek_wrap_alg, dek_wrap_nonce,
			 dek_wrap_version, recipient_key_version, access_kind, source_tunnel_id,
			 granted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'share', NULL, $8, $9, $10)
		ON CONFLICT (file_id, recipient_cns_user_id) DO NOTHING
	`, envelope.FileID, envelope.RecipientCNSUserID, envelope.WrappedDEK, envelope.DEKWrapAlg,
		envelope.DEKWrapNonce, envelope.DEKWrapVersion, envelope.RecipientKeyVersion,
		envelope.GrantedAt, envelope.CreatedAt, envelope.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrTransferExists
	}
	return tx.Commit()
}

// ListReceivedTransfers returns a recipient's transfers. view "pending"
// lists unanswered transfers whose file is still available; view "history"
// lists accepted and declined transfers, newest answer first.
func (p *Postgres) ListReceivedTransfers(ctx context.Context, recipientUserID int64, view string, page, perPage int) ([]models.TransferListItem, int, error) {
	where := `t.status = 'pending' AND COALESCE(f.is_deleted, FALSE) = FALSE AND f.expires_at > NOW()`
	order := `t.created_at DESC`
	if view == "history" {
		where = `t.status IN ('accepted', 'declined')`
		order = `COALESCE(t.responded_at, t.created_at) DESC`
	}

	var total int
	if err := p.db.GetContext(ctx, &total, `
		SELECT COUNT(*)
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		WHERE t.recipient_cns_user_id = $1 AND `+where, recipientUserID); err != nil {
		return nil, 0, err
	}

	items := []models.TransferListItem{}
	if err := p.db.SelectContext(ctx, &items, `
		SELECT f.id AS file_id, f.original_name AS filename, f.size_bytes, f.expires_at,
			t.status, t.created_at AS sent_at, t.responded_at,
			(COALESCE(f.is_deleted, FALSE) = FALSE AND f.expires_at > NOW()) AS available,
			t.sender_cns_user_id AS sender_user_id,
			COALESCE(u.username, f.owner_cns_username, '') AS sender_username,
			COALESCE(u.avatar_url, '') AS sender_avatar_url
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		LEFT JOIN users u ON u.cns_user_id = t.sender_cns_user_id
		WHERE t.recipient_cns_user_id = $1 AND `+where+`
		ORDER BY `+order+`
		LIMIT $2 OFFSET $3
	`, recipientUserID, perPage, (page-1)*perPage); err != nil {
		return nil, 0, err
	}
	for i := range items {
		if items[i].RespondedAtDB.Valid {
			t := items[i].RespondedAtDB.Time
			items[i].RespondedAt = &t
		}
	}
	return items, total, nil
}

// CountPendingTransfers counts a recipient's unanswered transfers whose file
// is still available (the account menu badge).
func (p *Postgres) CountPendingTransfers(ctx context.Context, recipientUserID int64) (int, error) {
	var count int
	err := p.db.GetContext(ctx, &count, `
		SELECT COUNT(*)
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		WHERE t.recipient_cns_user_id = $1 AND t.status = 'pending'
		  AND COALESCE(f.is_deleted, FALSE) = FALSE AND f.expires_at > NOW()
	`, recipientUserID)
	return count, err
}

// RespondToTransfer accepts or declines a pending transfer. Declining also
// deletes the recipient's key envelope, so the server can no longer hand
// them the wrapped DEK; the transfer row stays as history. Only the
// recipient can answer, and only once.
func (p *Postgres) RespondToTransfer(ctx context.Context, recipientUserID int64, fileID string, accept bool) (*models.Transfer, error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var row struct {
		models.Transfer
		Available bool `db:"available"`
	}
	err = tx.GetContext(ctx, &row, `
		SELECT t.id, t.file_id, t.sender_cns_user_id, t.recipient_cns_user_id, t.status,
			t.created_at, t.responded_at,
			(COALESCE(f.is_deleted, FALSE) = FALSE AND f.expires_at > NOW()) AS available
		FROM file_transfers t
		JOIN files f ON f.id = t.file_id
		WHERE t.file_id = $1 AND t.recipient_cns_user_id = $2
		FOR UPDATE OF t
	`, fileID, recipientUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrTransferNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.Status != models.TransferStatusPending {
		return nil, models.ErrTransferAlreadyAnswered
	}
	if accept && !row.Available {
		return nil, models.ErrTransferFileUnavailable
	}

	status := models.TransferStatusDeclined
	if accept {
		status = models.TransferStatusAccepted
	}
	if err := tx.GetContext(ctx, &row.RespondedAt, `
		UPDATE file_transfers SET status = $1, responded_at = NOW()
		WHERE id = $2
		RETURNING responded_at
	`, status, row.ID); err != nil {
		return nil, err
	}
	if !accept {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM file_access_key_envelopes
			WHERE file_id = $1 AND recipient_cns_user_id = $2 AND access_kind = 'share'
		`, fileID, recipientUserID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	row.Status = status
	return &row.Transfer, nil
}
