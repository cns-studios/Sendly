package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"

	"sendly/internal/middleware"
	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

// Transfers are user-to-user sends (see file_transfers). These endpoints
// never touch key material: accepting only unlocks the recipient's existing
// identity-wrapped envelope, declining deletes it.

// ListTransfers returns the caller's transfers: ?view=pending (default),
// the received transfers awaiting an answer, or ?view=history, filtered by
// ?direction=all (default), received or sent.
func (h *RecentUploadsHandler) ListTransfers(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	view := c.DefaultQuery("view", "pending")
	if view != "pending" && view != "history" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "view must be pending or history", Code: "INVALID_VIEW"})
		return
	}
	direction := c.DefaultQuery("direction", models.TransferDirectionAll)
	if direction != models.TransferDirectionAll && direction != models.TransferDirectionReceived && direction != models.TransferDirectionSent {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "direction must be all, received or sent", Code: "INVALID_DIRECTION"})
		return
	}
	page, perPage, ok := parsePagination(c)
	if !ok {
		return
	}
	items, total, err := h.db.ListTransfers(c.Request.Context(), int64(user.ID), view, direction, page, perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch transfers", Code: "TRANSFERS_FAILED"})
		return
	}
	totalPages := (total + perPage - 1) / perPage
	c.JSON(http.StatusOK, models.TransfersResponse{Items: items, Page: page, PerPage: perPage, Total: total, TotalPages: totalPages})
}

// PendingTransferCount backs the unread badge in the account menu.
func (h *RecentUploadsHandler) PendingTransferCount(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	count, err := h.db.CountPendingTransfers(c.Request.Context(), int64(user.ID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to count transfers", Code: "TRANSFERS_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *RecentUploadsHandler) AcceptTransfer(c *gin.Context) {
	h.respondToTransfer(c, true)
}

func (h *RecentUploadsHandler) DeclineTransfer(c *gin.Context) {
	h.respondToTransfer(c, false)
}

func (h *RecentUploadsHandler) respondToTransfer(c *gin.Context, accept bool) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}
	fileID := strings.TrimSpace(c.Param("file_id"))
	transfer, err := h.db.RespondToTransfer(c.Request.Context(), int64(user.ID), fileID, accept)
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case models.ErrTransferNotFound:
			status = http.StatusNotFound
		case models.ErrTransferAlreadyAnswered:
			status = http.StatusConflict
		case models.ErrTransferFileUnavailable:
			status = http.StatusGone
		}
		if appErr, ok := err.(*models.AppError); ok {
			c.JSON(status, models.ErrorResponse{Error: appErr.Message, Code: appErr.Code})
			return
		}
		c.JSON(status, models.ErrorResponse{Error: "Failed to update transfer", Code: "TRANSFER_UPDATE_FAILED"})
		return
	}
	h.publishTransfersChanged(c.Request.Context(), int64(user.ID))
	h.publishSentTransferChanged(transfer.SenderCNSUserID, transfer.FileID, transfer.Status)
	c.JSON(http.StatusOK, gin.H{"file_id": transfer.FileID, "status": transfer.Status})
}

// publishTransfersChanged pushes the recipient's current pending count over
// the existing per-user device websocket (/api/me/devices/ws), so the badge
// and an open transfers page update live.
func (h *RecentUploadsHandler) publishTransfersChanged(ctx context.Context, recipientUserID int64) {
	if h.hub == nil {
		return
	}
	count, err := h.db.CountPendingTransfers(ctx, recipientUserID)
	if err != nil {
		log.Printf("transfers: count for user %d failed: %v", recipientUserID, err)
		return
	}
	h.hub.broadcast(recipientUserID, gin.H{"type": "transfers_updated", "pending_count": count})
}

// publishSentTransferChanged tells the sender's open pages that one of their
// sent transfers was created or answered, so their transfer history can
// refresh. It carries no count: sent transfers never raise the badge.
func (h *RecentUploadsHandler) publishSentTransferChanged(senderUserID int64, fileID, status string) {
	if h.hub == nil {
		return
	}
	h.hub.broadcast(senderUserID, gin.H{"type": "sent_transfers_updated", "file_id": fileID, "status": status})
}
