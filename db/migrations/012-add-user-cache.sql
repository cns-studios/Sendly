CREATE TABLE IF NOT EXISTS users (
    cns_user_id BIGINT PRIMARY KEY,
    username TEXT NOT NULL,
    avatar_url TEXT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deactivated_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_users_status_last_synced ON users(status, last_synced_at);
