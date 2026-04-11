CREATE TABLE IF NOT EXISTS file_key_envelopes (
    file_id VARCHAR(20) PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    wrapped_dek BYTEA NOT NULL,
    dek_wrap_alg TEXT NOT NULL,
    dek_wrap_nonce BYTEA NULL,
    dek_wrap_version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_key_envelopes (
    id UUID PRIMARY KEY,
    cns_user_id BIGINT NOT NULL,
    device_id UUID NOT NULL REFERENCES user_devices(id) ON DELETE CASCADE,
    wrapped_user_key BYTEA NOT NULL,
    uk_wrap_alg TEXT NOT NULL,
    uk_wrap_meta JSONB NOT NULL,
    key_version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (cns_user_id, device_id)
);
