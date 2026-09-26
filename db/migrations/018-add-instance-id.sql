-- A random ID identifying this database. The server writes it into a marker
-- file in DATA_DIR (and CHUNK_DIR) and refuses to start when the marker
-- belongs to another database, so two instances never share file storage:
-- each one's orphan cleanup would delete the other's blobs.
CREATE TABLE IF NOT EXISTS instance_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO instance_meta (key, value)
VALUES ('instance_id', gen_random_uuid()::text)
ON CONFLICT (key) DO NOTHING;
