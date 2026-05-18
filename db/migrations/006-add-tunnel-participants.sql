CREATE TABLE IF NOT EXISTS tunnel_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tunnel_id UUID NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    cns_user_id BIGINT NULL,
    device_id UUID NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tunnel_id, cns_user_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_tunnel_participants_tunnel_id ON tunnel_participants(tunnel_id);
