CREATE TABLE IF NOT EXISTS tunnels (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    initiator_cns_user_id BIGINT NOT NULL,
    initiator_device_id UUID NULL,
    peer_cns_user_id BIGINT NULL,
    peer_device_id UUID NULL,
    duration_minutes INT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    initiator_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    peer_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ NULL,
    ended_at TIMESTAMPTZ NULL,
    ended_by_cns_user_id BIGINT NULL,
    ended_by_device_id UUID NULL
);

ALTER TABLE files
    ADD COLUMN IF NOT EXISTS tunnel_id UUID NULL REFERENCES tunnels(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_files_tunnel_created_at ON files(tunnel_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tunnels_code ON tunnels(code);
CREATE INDEX IF NOT EXISTS idx_tunnels_expires_at ON tunnels(expires_at);