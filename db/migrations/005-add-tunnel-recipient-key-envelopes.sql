CREATE TABLE IF NOT EXISTS file_recipient_key_envelopes (
    file_id VARCHAR(20) NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    recipient_cns_user_id BIGINT NOT NULL,
    recipient_device_id UUID NOT NULL,
    wrapped_dek BYTEA NOT NULL,
    dek_wrap_alg TEXT NOT NULL,
    dek_wrap_nonce BYTEA NULL,
    dek_wrap_version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (file_id, recipient_cns_user_id, recipient_device_id)
);

CREATE INDEX IF NOT EXISTS idx_file_recipient_key_envelopes_recipient
    ON file_recipient_key_envelopes(recipient_cns_user_id, recipient_device_id);
