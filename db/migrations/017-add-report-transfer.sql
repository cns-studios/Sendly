-- Link a report to the transfer it was filed from. Recipients can report a
-- file they accepted through a transfer; the transfer shows moderators who
-- sent it to whom. NULL for reports filed from a share link.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS transfer_id UUID NULL
    REFERENCES file_transfers(id) ON DELETE SET NULL;
