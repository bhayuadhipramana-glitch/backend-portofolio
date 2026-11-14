-- 000003_create_sessions_table.up.sql
-- Server-side admin sessions. Only the SHA-256 hash of the session token is
-- stored, so a database leak does not expose usable cookies.

CREATE TABLE IF NOT EXISTS sessions (
    token_hash CHAR(64)    PRIMARY KEY,
    github_id  BIGINT      NOT NULL,
    login      VARCHAR(100) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
