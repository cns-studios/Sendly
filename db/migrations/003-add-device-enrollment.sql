CREATE TABLE IF NOT EXISTS device_enrollments (
    id UUID PRIMARY KEY,
    cns_user_id BIGINT NOT NULL,
    request_device_id UUID NOT NULL REFERENCES user_devices(id) ON DELETE CASCADE,
    verification_code VARCHAR(16) NOT NULL,
    status TEXT NOT NULL,
    approved_by_device_id UUID NULL REFERENCES user_devices(id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_device_enrollments_user_status_created ON device_enrollments(cns_user_id, status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_enrollments_one_pending_per_device
ON device_enrollments(request_device_id)
WHERE status = 'pending';
