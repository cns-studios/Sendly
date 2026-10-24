-- Device kind. "approver" is the CNS Accounts app: it holds the identity key
-- only to approve new devices, can't set up or recover the identity key, and
-- can remove itself.
ALTER TABLE user_devices
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'standard';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_devices_kind_check') THEN
        ALTER TABLE user_devices ADD CONSTRAINT user_devices_kind_check CHECK (kind IN ('standard', 'approver'));
    END IF;
END $$;
