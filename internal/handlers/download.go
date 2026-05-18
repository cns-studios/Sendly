package handlers

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"shareit/internal/config"
	"shareit/internal/models"
	"shareit/internal/storage"

	"github.com/gin-gonic/gin"
)

type DownloadHandler struct {
	cfg *config.Config
	db  *storage.Postgres
	fs  *storage.Filesystem
}

func NewDownloadHandler(cfg *config.Config, db *storage.Postgres, fs *storage.Filesystem) *DownloadHandler {
	return &DownloadHandler{
		cfg: cfg,
		db:  db,
		fs:  fs,
	}
}

 
func (h *DownloadHandler) GetMetadata(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing file ID",
			Code:  "MISSING_FILE_ID",
		})
		return
	}

	 
	if !isValidFileID(fileID) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Invalid file ID format",
			Code:  "INVALID_FILE_ID",
		})
		return
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

	 
	if !h.fs.FileExists(fileID) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: "File not found on storage",
			Code:  "FILE_NOT_ON_DISK",
		})
		return
	}

	c.JSON(http.StatusOK, file.ToMetadata())
}

 
func (h *DownloadHandler) Download(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing file ID",
			Code:  "MISSING_FILE_ID",
		})
		return
	}

	if !isValidFileID(fileID) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Invalid file ID format",
			Code:  "INVALID_FILE_ID",
		})
		return
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

	reader, err := h.fs.GetFileReader(fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: "File not found on storage",
			Code:  "FILE_NOT_ON_DISK",
		})
		return
	}
	defer reader.Close()

	fileSize, err := h.fs.GetFileSize(fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: "Failed to get file size",
			Code:  "FILE_SIZE_ERROR",
		})
		return
	}

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.enc\"", fileID))
	c.Header("Content-Length", fmt.Sprintf("%d", fileSize))
	c.Header("X-Original-Filename", file.OriginalName)
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")

	c.Status(http.StatusOK)
	c.Writer.Flush()
	_, err = io.Copy(c.Writer, reader)
	if err != nil {
		fmt.Printf("Error streaming file %s: %v\n", fileID, err)
	}
}

 
func (h *DownloadHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Missing numeric code",
			Code:  "MISSING_CODE",
		})
		return
	}

	 
	if !isValidNumericCode(code) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "Invalid numeric code format. Must be 12 digits.",
			Code:  "INVALID_CODE_FORMAT",
		})
		return
	}

	 
	file, err := h.db.GetFileByNumericCode(c.Request.Context(), code)
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

	 
	if !h.fs.FileExists(file.ID) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: "File not found on storage",
			Code:  "FILE_NOT_ON_DISK",
		})
		return
	}

	c.JSON(http.StatusOK, file.ToMetadata())
}

 
func isValidFileID(id string) bool {
	 
	if len(id) != 17 {
		return false
	}
	matched, _ := regexp.MatchString("^[a-z0-9]+$", id)
	return matched
}

 
func isValidNumericCode(code string) bool {
	if len(code) != 12 {
		return false
	}
	matched, _ := regexp.MatchString("^[0-9]+$", code)
	return matched
}