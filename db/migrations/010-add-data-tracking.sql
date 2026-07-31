CREATE TABLE IF NOT EXISTS data_counters (
    key TEXT PRIMARY KEY,
    value BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO data_counters (key, value)
VALUES ('total_uploaded', 0), ('total_processed', 0)
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS uploads_by_ip (
    ip TEXT PRIMARY KEY,
    uploaded_bytes BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
