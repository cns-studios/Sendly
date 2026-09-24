package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"sendly/internal/config"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/services"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

type ReportHandler struct {
	cfg     *config.Config
	db      *storage.Postgres
	discord *services.Discord
}

func NewReportHandler(cfg *config.Config, db *storage.Postgres, discord *services.Discord) *ReportHandler {
	return &ReportHandler{
		cfg:     cfg,
		db:      db,
		discord: discord,
	}
}

 
func (h *ReportHandler) Report(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing file ID",
			Code:  "MISSING_FILE_ID",
		})
		return
	}

	 
	reporterIP := middleware.GetClientIP(c)
	var reporterUserID int64
	if user := middleware.GetCNSUser(c); user != nil && user.ID > 0 {
		reporterUserID = int64(user.ID)
	}

	 
	file, err := h.db.GetFileByID(c.Request.Context(), fileID)
	if err != nil {
		if appErr, ok := err.(*models.AppError); ok {
			status := http.StatusNotFound
			if appErr == models.ErrFileExpired || appErr == models.ErrFileDeleted {
				status = http.StatusGone
			}
			c.JSON(status, models.ErrorResponse{
				Error: appErr.Message,
				Code:  appErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to get file",
			Code:  "GET_FILE_FAILED",
		})
		return
	}

	 
	hasReported, err := h.db.HasUserReportedFile(c.Request.Context(), fileID, reporterIP, reporterUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to check report status",
			Code:  "CHECK_REPORT_FAILED",
		})
		return
	}

	if hasReported {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Error: "You have already reported this file",
			Code:  "ALREADY_REPORTED",
		})
		return
	}

	 
	report := &models.Report{
		FileID:     fileID,
		ReporterIP: reporterIP,
		CreatedAt:  time.Now(),
	}
	if reporterUserID > 0 {
		report.ReporterCNSUserID = sql.NullInt64{Int64: reporterUserID, Valid: true}
	}

	created, err := h.db.CreateReport(c.Request.Context(), report)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to create report",
			Code:  "CREATE_REPORT_FAILED",
		})
		return
	}
	if !created {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Error: "You have already reported this file",
			Code:  "ALREADY_REPORTED",
		})
		return
	}

	 
	newReportCount, err := h.db.IncrementReportCount(c.Request.Context(), fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to update report count",
			Code:  "UPDATE_REPORT_FAILED",
		})
		return
	}

	 
	file.ReportCount = newReportCount

	 
	if err := h.discord.SendReportNotification(file, reporterIP, newReportCount); err != nil {
		 
		println("Failed to send Discord notification:", err.Error())
	}

	 
	// Only distinct signed-in reporters count towards automatic deletion;
	// anonymous reports are recorded and forwarded to moderation, but on
	// their own must not let an unauthenticated client delete any file.
	signedInReporters, err := h.db.CountSignedInReporters(c.Request.Context(), fileID)
	if err != nil {
		println("Failed to count signed-in reporters:", err.Error())
		signedInReporters = 0
	}

	if signedInReporters >= h.cfg.AutoDeleteReportCount {
		 
		if err := h.db.MarkFileDeleted(c.Request.Context(), fileID); err != nil {
			 
			println("Failed to mark file as deleted:", err.Error())
		} else {
			 
			if err := h.discord.SendAutoDeleteNotification(file); err != nil {
				println("Failed to send auto-delete notification:", err.Error())
			}
		}

		c.JSON(http.StatusOK, models.ReportResponse{
			Success: true,
			Message: "File has been reported and automatically removed due to multiple reports",
		})
		return
	}

	c.JSON(http.StatusOK, models.ReportResponse{
		Success: true,
		Message: "File has been reported. Thank you for helping keep our platform safe.",
	})
}