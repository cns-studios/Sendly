-- Anonymous quick share participants are authenticated by a random secret
-- issued when they join; only its SHA-256 (hex) is stored. Signed-in
-- participants are authenticated by their CNS user instead and leave this
-- NULL. A participant row can only be re-joined (e.g. to replace the key)
-- by its owner.
ALTER TABLE tunnel_participants ADD COLUMN IF NOT EXISTS participant_token_hash TEXT NULL;
