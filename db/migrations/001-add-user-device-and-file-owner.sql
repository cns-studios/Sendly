ALTER TABLE files
    ADD COLUMN IF NOT EXISTS owner_cns_user_id BIGINT NULL,
    ADD COLUMN IF NOT EXISTS owner_cns_username TEXT NULL;

CREATE TABLE IF NOT EXISTS user_devices (
    id UUID PRIMARY KEY,
    cns_user_id BIGINT NOT NULL,
    device_label TEXT NULL,
    public_key_jwk JSONB NOT NULL,
    key_algorithm TEXT NOT NULL,
    key_version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_files_owner_created_at ON files(owner_cns_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_devices_user_revoked ON user_devices(cns_user_id, revoked_at);
