-- Record which signed-in user filed a report. Automatic deletion counts only
-- distinct signed-in reporters, so anonymous (IP-only) reports can no longer
-- take a file down on their own; each user can report a file once.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS reporter_cns_user_id BIGINT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_reports_file_reporter_user
    ON reports(file_id, reporter_cns_user_id)
    WHERE reporter_cns_user_id IS NOT NULL;
