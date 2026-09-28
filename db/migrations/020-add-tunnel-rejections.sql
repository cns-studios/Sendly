-- Joiners a quick share host declined; they can't join that session again.
-- Signed-in users are matched by account, guests by device ID (a guest who
-- clears their browser gets a new device ID, so this only stops casual
-- rejoining for guests).
CREATE TABLE IF NOT EXISTS tunnel_rejections (
    id BIGSERIAL PRIMARY KEY,
    tunnel_id UUID NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    cns_user_id BIGINT NULL,
    device_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tunnel_rejections_tunnel ON tunnel_rejections(tunnel_id);
