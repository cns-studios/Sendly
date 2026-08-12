package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
)

type Discord struct {
	webhookURL string
	baseURL    string
	httpClient *http.Client
}

func NewDiscord(cfg *config.Config) *Discord {
	return &Discord{
		webhookURL: cfg.DiscordWebhookURL,
		baseURL:    cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

 
func (d *Discord) IsConfigured() bool {
	return d.webhookURL != ""
}

 
func (d *Discord) SendReportNotification(file *models.File, reporterIP string, reportCount int) error {
	if !d.IsConfigured() {
		return nil
	}

	shareURL := fmt.Sprintf("%s/shared/%s", d.baseURL, file.ID)

	payload := models.DiscordWebhookPayload{
		Content: "@everyone",
		Embeds: []models.Embed{
			{
				Title: "File Reported",
				Color: 15158332,
				Fields: []models.Field{
					{
						Name:   "File ID",
						Value:  fmt.Sprintf("`%s`", file.ID),
						Inline: true,
					},
					{
						Name:   "Numeric Code",
						Value:  fmt.Sprintf("`%s`", file.NumericCode),
						Inline: true,
					},
					{
						Name:   "Report Count",
						Value:  fmt.Sprintf("`%d`", reportCount),
						Inline: true,
					},
					{
						Name:   "File Name",
						Value:  fmt.Sprintf("`%s`", file.OriginalName),
						Inline: false,
					},
					{
						Name:   "File Size",
						Value:  fmt.Sprintf("`%s`", formatFileSize(file.SizeBytes)),
						Inline: true,
					},
					{
						Name:   "Uploaded",
						Value:  fmt.Sprintf("<t:%d:R>", file.CreatedAt.Unix()),
						Inline: true,
					},
					{
						Name:   "Expires",
						Value:  fmt.Sprintf("<t:%d:R>", file.ExpiresAt.Unix()),
						Inline: true,
					},
					{
						Name:   "Uploader IP",
						Value:  fmt.Sprintf("`%s`", file.UploaderIP),
						Inline: true,
					},
					{
						Name:   "Reporter IP",
						Value:  fmt.Sprintf("`%s`", reporterIP),
						Inline: true,
					},
					{
						Name:   "Share Link",
						Value:  shareURL,
						Inline: false,
					},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	return d.sendWebhook(payload)
}

 
func (d *Discord) SendAutoDeleteNotification(file *models.File) error {
	if !d.IsConfigured() {
		return nil
	}

	payload := models.DiscordWebhookPayload{
		Content: "@everyone",
		Embeds: []models.Embed{
			{
				Title:       "File Auto-Deleted",
				Description: "File was automatically deleted due to exceeding report threshold.",
				Color:       10038562,  
				Fields: []models.Field{
					{
						Name:   "File ID",
						Value:  fmt.Sprintf("`%s`", file.ID),
						Inline: true,
					},
					{
						Name:   "Numeric Code",
						Value:  fmt.Sprintf("`%s`", file.NumericCode),
						Inline: true,
					},
					{
						Name:   "Report Count",
						Value:  fmt.Sprintf("`%d`", file.ReportCount),
						Inline: true,
					},
					{
						Name:   "File Name",
						Value:  fmt.Sprintf("`%s`", file.OriginalName),
						Inline: false,
					},
					{
						Name:   "Uploader IP",
						Value:  fmt.Sprintf("`%s`", file.UploaderIP),
						Inline: true,
					},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	return d.sendWebhook(payload)
}

 
func (d *Discord) SendAdminFileNotification(file *models.File, action string) error {
	if !d.IsConfigured() {
		return nil
	}

	var color int
	var title string

	switch action {
	case "view":
		color = 3447003  
		title = "Admin File View"
	case "delete":
		color = 15158332  
		title = "Admin File Delete"
	case "download":
		color = 3066993  
		title = "Admin File Download"
	default:
		color = 9807270  
		title = "Admin Action"
	}

	payload := models.DiscordWebhookPayload{
		Embeds: []models.Embed{
			{
				Title: title,
				Color: color,
				Fields: []models.Field{
					{
						Name:   "File ID",
						Value:  fmt.Sprintf("`%s`", file.ID),
						Inline: true,
					},
					{
						Name:   "Numeric Code",
						Value:  fmt.Sprintf("`%s`", file.NumericCode),
						Inline: true,
					},
					{
						Name:   "Status",
						Value:  fmt.Sprintf("`Deleted: %t`", file.IsDeleted),
						Inline: true,
					},
					{
						Name:   "File Name",
						Value:  fmt.Sprintf("`%s`", file.OriginalName),
						Inline: false,
					},
					{
						Name:   "File Size",
						Value:  fmt.Sprintf("`%s`", formatFileSize(file.SizeBytes)),
						Inline: true,
					},
					{
						Name:   "Report Count",
						Value:  fmt.Sprintf("`%d`", file.ReportCount),
						Inline: true,
					},
					{
						Name:   "Uploader IP",
						Value:  fmt.Sprintf("`%s`", file.UploaderIP),
						Inline: true,
					},
					{
						Name:   "Uploaded",
						Value:  fmt.Sprintf("<t:%d:f>", file.CreatedAt.Unix()),
						Inline: true,
					},
					{
						Name:   "Expires",
						Value:  fmt.Sprintf("<t:%d:f>", file.ExpiresAt.Unix()),
						Inline: true,
					},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	return d.sendWebhook(payload)
}

 
func (d *Discord) SendCleanupNotification(expiredCount, orphanedCount int64) error {
	if !d.IsConfigured() {
		return nil
	}

	 
	if expiredCount == 0 && orphanedCount == 0 {
		return nil
	}

	payload := models.DiscordWebhookPayload{
		Embeds: []models.Embed{
			{
				Title: "Cleanup Completed",
				Color: 7506394,  
				Fields: []models.Field{
					{
						Name:   "Expired Files Deleted",
						Value:  fmt.Sprintf("`%d`", expiredCount),
						Inline: true,
					},
					{
						Name:   "Orphaned Chunks Cleaned",
						Value:  fmt.Sprintf("`%d`", orphanedCount),
						Inline: true,
					},
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	return d.sendWebhook(payload)
}

 
func (d *Discord) sendWebhook(payload models.DiscordWebhookPayload) error {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequest("POST", d.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

 
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}