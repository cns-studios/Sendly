-- Temporary, for migrating accounts from before identity keys. Each account's
-- identity private key, encrypted in the browser with the account's legacy
-- AES user key, so every device that still holds the user key can pick up
-- the identity key without being approved again. The server can't read it.
-- Drop together with user_key_envelopes once every account is migrated.
CREATE TABLE IF NOT EXISTS legacy_identity_escrow (
    cns_user_id BIGINT PRIMARY KEY,
    identity_key_version INT NOT NULL,
    wrapped_private_key BYTEA NOT NULL,
    wrap_nonce BYTEA NOT NULL,
    wrap_alg TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (cns_user_id, identity_key_version)
        REFERENCES user_identity_keys(cns_user_id, key_version)
        ON DELETE CASCADE
);
