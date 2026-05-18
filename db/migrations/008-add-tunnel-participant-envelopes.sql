CREATE TABLE IF NOT EXISTS tunnel_participant_envelopes (
    tunnel_id              UUID    NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    participant_device_id  TEXT    NOT NULL,
    wrapped_dek            BYTEA   NOT NULL,
    dek_wrap_alg           TEXT    NOT NULL,
    dek_wrap_nonce         BYTEA   NULL,
    dek_wrap_version       INT     NOT NULL DEFAULT 1,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tunnel_id, participant_device_id)
);