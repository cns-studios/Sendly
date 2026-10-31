-- Keeps the CNS user cache current from CNS webhooks and the users:read
-- directory, instead of only when a user signs in again.
--
-- status gains 'deleted': CNS confirmed the account is gone. It is final:
-- username and avatar are erased, and no later sync, webhook or directory
-- answer brings the row back. The row stays as a tombstone so an event that
-- arrives out of order can't recreate the user.
--
-- inactive_reason says why a row is inactive: 'locked' (CNS reported the
-- account locked) or 'stale' (not confirmed against CNS for
-- USER_CACHE_STALE_AFTER_DAYS). Only 'stale' rows come back on their own
-- when the directory or a webhook confirms the account still exists.
--
-- profile_observed_at is when the cached username and avatar were true on
-- CNS: the event time for webhooks, the request start for direct lookups.
-- A profile observed earlier never overwrites one observed later, so
-- webhooks may arrive late or out of order.
ALTER TABLE users ADD COLUMN IF NOT EXISTS inactive_reason TEXT NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS profile_observed_at TIMESTAMPTZ NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL;

UPDATE users SET inactive_reason = 'stale'
WHERE status = 'inactive' AND inactive_reason IS NULL;

UPDATE users SET profile_observed_at = last_synced_at
WHERE profile_observed_at IS NULL;

-- CNS allows one live account per username (case-insensitive) but frees the
-- name on deletion, so this index is not unique: two cached rows can hold the
-- same name until the older one is re-checked.
CREATE INDEX IF NOT EXISTS idx_users_lower_username ON users (LOWER(username));
CREATE INDEX IF NOT EXISTS idx_users_last_synced_live ON users (last_synced_at)
    WHERE status <> 'deleted';

-- Webhook event ids already applied. CNS retries a delivery for about a day
-- and keeps the event id across retries, so ids are kept for a week.
CREATE TABLE IF NOT EXISTS cns_webhook_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cns_webhook_events_received_at ON cns_webhook_events (received_at);
