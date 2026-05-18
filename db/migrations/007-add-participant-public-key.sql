ALTER TABLE tunnel_participants
    ADD COLUMN IF NOT EXISTS public_key_jwk  JSONB        NULL,
    ADD COLUMN IF NOT EXISTS key_algorithm   TEXT         NULL,
    ADD COLUMN IF NOT EXISTS key_version     INT          NULL;