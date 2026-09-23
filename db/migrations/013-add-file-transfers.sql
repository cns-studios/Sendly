-- Transfers: user-to-user file sharing with explicit accept/decline.
--
-- The wrapped file key for a transfer recipient lives in
-- file_access_key_envelopes (access_kind = 'share'), wrapped client-side with
-- the recipient's identity public key; the server never sees the DEK. This
-- table only tracks the transfer's lifecycle. The key is handed out only once
-- the transfer is accepted, and a decline deletes the recipient's envelope
-- (revoking the key) while this row remains as history.
--
-- A file can be transferred to many users, but only once per recipient:
-- UNIQUE (file_id, recipient_cns_user_id) holds even after a decline.
CREATE TABLE IF NOT EXISTS file_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id VARCHAR(20) NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    sender_cns_user_id BIGINT NOT NULL,
    recipient_cns_user_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ NULL,
    UNIQUE (file_id, recipient_cns_user_id)
);

CREATE INDEX IF NOT EXISTS idx_file_transfers_recipient_status
    ON file_transfers(recipient_cns_user_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_file_transfers_sender_created
    ON file_transfers(sender_cns_user_id, created_at DESC);

-- Shares made before transfers existed had no accept step; keep them
-- accessible by recording them as already accepted.
INSERT INTO file_transfers (file_id, sender_cns_user_id, recipient_cns_user_id, status, created_at, responded_at)
SELECT e.file_id, f.owner_cns_user_id, e.recipient_cns_user_id, 'accepted', e.granted_at, e.granted_at
FROM file_access_key_envelopes e
JOIN files f ON f.id = e.file_id
WHERE e.access_kind = 'share' AND f.owner_cns_user_id IS NOT NULL
ON CONFLICT (file_id, recipient_cns_user_id) DO NOTHING;
