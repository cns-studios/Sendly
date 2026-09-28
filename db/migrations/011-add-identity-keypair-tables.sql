CREATE TABLE IF NOT EXISTS user_identity_keys (
    cns_user_id BIGINT NOT NULL,
    key_version INT NOT NULL,
    public_key_jwk JSONB NOT NULL,
    key_algorithm TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ NULL,
    retired_at TIMESTAMPTZ NULL,
    PRIMARY KEY (cns_user_id, key_version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identity_keys_one_active
    ON user_identity_keys(cns_user_id)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS user_identity_key_device_envelopes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cns_user_id BIGINT NOT NULL,
    device_id UUID NOT NULL REFERENCES user_devices(id) ON DELETE CASCADE,
    identity_key_version INT NOT NULL,
    wrapped_private_key BYTEA NOT NULL,
    wrap_alg TEXT NOT NULL,
    wrap_meta JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (cns_user_id, device_id, identity_key_version),
    FOREIGN KEY (cns_user_id, identity_key_version)
        REFERENCES user_identity_keys(cns_user_id, key_version)
        ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS file_access_key_envelopes (
    file_id VARCHAR(20) NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    recipient_cns_user_id BIGINT NOT NULL,
    wrapped_dek BYTEA NOT NULL,
    dek_wrap_alg TEXT NOT NULL,
    dek_wrap_nonce BYTEA NULL,
    dek_wrap_version INT NOT NULL DEFAULT 1,
    recipient_key_version INT NOT NULL,
    access_kind TEXT NOT NULL CHECK (access_kind IN ('owner', 'share')),
    source_tunnel_id UUID NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (file_id, recipient_cns_user_id),
    FOREIGN KEY (recipient_cns_user_id, recipient_key_version)
        REFERENCES user_identity_keys(cns_user_id, key_version)
);

CREATE INDEX IF NOT EXISTS idx_file_access_key_envelopes_recipient
    ON file_access_key_envelopes(recipient_cns_user_id);

CREATE INDEX IF NOT EXISTS idx_file_access_key_envelopes_source_tunnel
    ON file_access_key_envelopes(source_tunnel_id);
