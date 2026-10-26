-- Baseline schema, formerly init.sql (run only by the Postgres container on
-- first start). As a migration it also reaches databases that are not created
-- by that container, such as a CloudNativePG cluster. Every statement is
-- idempotent, so databases created from init.sql apply it as a no-op.

-- Create files table
CREATE TABLE IF NOT EXISTS files (
    id VARCHAR(20) PRIMARY KEY,
    numeric_code VARCHAR(12) UNIQUE NOT NULL,
    original_name TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    uploader_ip VARCHAR(45),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    report_count INT DEFAULT 0,
    is_deleted BOOLEAN DEFAULT FALSE
);

-- Create reports table
CREATE TABLE IF NOT EXISTS reports (
    id SERIAL PRIMARY KEY,
    file_id VARCHAR(20) REFERENCES files(id) ON DELETE CASCADE,
    reporter_ip VARCHAR(45),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_files_numeric_code ON files(numeric_code);
CREATE INDEX IF NOT EXISTS idx_files_expires_at ON files(expires_at);
CREATE INDEX IF NOT EXISTS idx_files_is_deleted ON files(is_deleted);
CREATE INDEX IF NOT EXISTS idx_files_created_at ON files(created_at);
CREATE INDEX IF NOT EXISTS idx_reports_file_id ON reports(file_id);


-- Desktop API keys
CREATE TABLE IF NOT EXISTS desktop_api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_value TEXT UNIQUE NOT NULL,
    owner_name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    is_active BOOLEAN DEFAULT TRUE
);

-- Associates a file with a desktop API key (ownership)
CREATE TABLE IF NOT EXISTS desktop_files (
    file_id VARCHAR(20) REFERENCES files(id) ON DELETE CASCADE,
    api_key_id UUID REFERENCES desktop_api_keys(id) ON DELETE CASCADE,
    PRIMARY KEY (file_id, api_key_id)
);

CREATE INDEX IF NOT EXISTS idx_desktop_files_api_key_id ON desktop_files(api_key_id);
CREATE INDEX IF NOT EXISTS idx_desktop_files_file_id ON desktop_files(file_id);
CREATE INDEX IF NOT EXISTS idx_desktop_api_keys_key_value ON desktop_api_keys(key_value);


-- Function to auto-delete files when report count exceeds threshold
-- This will be called from the application, not as a trigger
-- (threshold is configurable via environment variable)

-- Create a function to clean up expired files (called by app)
CREATE OR REPLACE FUNCTION cleanup_expired_files()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    WITH deleted AS (
        UPDATE files 
        SET is_deleted = TRUE 
        WHERE expires_at < NOW() 
        AND is_deleted = FALSE
        RETURNING id
    )
    SELECT COUNT(*) INTO deleted_count FROM deleted;
    
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;
