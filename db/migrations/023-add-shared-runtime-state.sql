-- Runtime state that used to live in Redis or in one process's memory. Every
-- replica must see it, and it has to survive a replica being killed, so it
-- moves into Postgres. None of it is key material: chunks stay ciphertext.

-- One row per chunked upload, from init until it expires. state:
--   uploading  -> chunks arriving
--   assembling -> complete was called; a replica holding the lease joins the chunks
--   assembled  -> the blob is in place and waits for finalize
--   finalized  -> the files row exists; kept until expiry so a retried finalize gets the same answer
--   failed     -> assembly failed for good; error holds the message
CREATE TABLE IF NOT EXISTS upload_sessions (
    session_id TEXT PRIMARY KEY,
    file_id VARCHAR(20) NOT NULL UNIQUE,
    original_name TEXT NOT NULL,
    total_size BIGINT NOT NULL,
    total_chunks INT NOT NULL,
    chunk_size BIGINT NOT NULL,
    uploader_ip VARCHAR(45),
    state TEXT NOT NULL DEFAULT 'uploading',
    error TEXT,
    assembly_owner TEXT,
    assembly_lease_until TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires_at ON upload_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_assembling ON upload_sessions(assembly_lease_until)
    WHERE state = 'assembling';

CREATE TABLE IF NOT EXISTS upload_chunks (
    session_id TEXT NOT NULL REFERENCES upload_sessions(session_id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    PRIMARY KEY (session_id, chunk_index)
);

-- Fixed-window request counters. Unlogged: losing them in a crash or failover
-- only resets the windows.
CREATE UNLOGGED TABLE IF NOT EXISTS rate_limit_counters (
    key TEXT PRIMARY KEY,
    window_start TIMESTAMPTZ NOT NULL,
    count BIGINT NOT NULL
);

-- The outcome of a CNS token refresh, kept for a few seconds so concurrent
-- requests on any replica presenting the same refresh token reuse it instead
-- of refreshing twice (which CNS treats as token theft). token_id is a hash
-- of the refresh token; sealed is the result encrypted under a key derived
-- from the refresh token, so the rows are useless without the token itself.
CREATE UNLOGGED TABLE IF NOT EXISTS token_refresh_results (
    token_id BYTEA PRIMARY KEY,
    sealed BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

-- Last start of each periodic job, so that only one replica runs it per interval.
CREATE TABLE IF NOT EXISTS job_runs (
    name TEXT PRIMARY KEY,
    last_run_at TIMESTAMPTZ NOT NULL
);
