package config

import (
	"testing"
	"time"
)

func TestRetentionDefaults(t *testing.T) {
	for _, key := range []string{"FILE_RECORD_RETENTION_DAYS", "UPLOAD_IP_RETENTION_DAYS", "REPORT_RETENTION_DAYS", "TUNNEL_REJECTION_RETENTION_DAYS"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	if cfg.FileRecordRetention != 30*day || cfg.UploadIPRetention != 30*day ||
		cfg.ReportRetention != 90*day || cfg.TunnelRejectionRetention != 30*day {
		t.Fatalf("unexpected defaults: %v %v %v %v", cfg.FileRecordRetention, cfg.UploadIPRetention, cfg.ReportRetention, cfg.TunnelRejectionRetention)
	}
}

func TestRetentionOverrides(t *testing.T) {
	t.Setenv("FILE_RECORD_RETENTION_DAYS", "7")
	t.Setenv("UPLOAD_IP_RETENTION_DAYS", "0")
	t.Setenv("REPORT_RETENTION_DAYS", "-5")
	t.Setenv("TUNNEL_REJECTION_RETENTION_DAYS", "abc")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	if cfg.FileRecordRetention != 7*day {
		t.Errorf("FileRecordRetention = %v", cfg.FileRecordRetention)
	}
	if cfg.UploadIPRetention != 0 {
		t.Errorf("0 should disable the purge, got %v", cfg.UploadIPRetention)
	}
	if cfg.ReportRetention != 90*day || cfg.TunnelRejectionRetention != 30*day {
		t.Errorf("invalid values should fall back to defaults: %v %v", cfg.ReportRetention, cfg.TunnelRejectionRetention)
	}
}
