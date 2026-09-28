-- Quick share joiners must be approved by the host before the host's client
-- wraps the session key for them or any file key is wrapped for them as the
-- tunnel peer. The host's own row is approved when the tunnel is created.
-- Rows that exist at migration time (tunnels already in progress) are
-- treated as approved so running sessions keep working.
ALTER TABLE tunnel_participants ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ NULL;

UPDATE tunnel_participants SET approved_at = joined_at WHERE approved_at IS NULL;
