-- Retention support. Soft-deleted files need a deletion time so their rows can
-- be hard-deleted after FILE_RECORD_RETENTION_DAYS.
ALTER TABLE files ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL;

-- Rows deleted before this column existed: an expired file was deleted no
-- earlier than its expiry; anything else (reported) starts its grace period now.
UPDATE files SET deleted_at = LEAST(expires_at, NOW())
WHERE is_deleted = TRUE AND deleted_at IS NULL;

-- Reports are kept for REPORT_RETENTION_DAYS, which can outlast the file row,
-- so they must not cascade away with it.
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_file_id_fkey;

CREATE INDEX IF NOT EXISTS idx_files_deleted_at ON files(deleted_at) WHERE is_deleted = TRUE;
CREATE INDEX IF NOT EXISTS idx_uploads_by_ip_updated_at ON uploads_by_ip(updated_at);
CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at);
CREATE INDEX IF NOT EXISTS idx_tunnel_rejections_created_at ON tunnel_rejections(created_at);
