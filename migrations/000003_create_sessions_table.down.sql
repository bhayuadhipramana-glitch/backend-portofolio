-- 000003_create_sessions_table.down.sql

DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP TABLE IF EXISTS sessions;
