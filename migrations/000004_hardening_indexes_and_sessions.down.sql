-- 000004_hardening_indexes_and_sessions.down.sql

ALTER TABLE sessions
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS revoked_at;

DROP INDEX IF EXISTS idx_resumes_active_created;
DROP INDEX IF EXISTS idx_profiles_active_created;
DROP INDEX IF EXISTS idx_articles_published;
DROP INDEX IF EXISTS idx_projects_created_at;

CREATE INDEX IF NOT EXISTS idx_articles_slug ON articles USING btree (slug);
