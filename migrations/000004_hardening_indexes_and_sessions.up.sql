-- 000004_hardening_indexes_and_sessions.up.sql
-- List-query indexes for the < 50 ms p99 read target and session revocation.

-- articles.slug is already UNIQUE, which creates its own index.
DROP INDEX IF EXISTS idx_articles_slug;

-- Each index matches the ORDER BY / WHERE of one list query.
CREATE INDEX IF NOT EXISTS idx_projects_created_at
    ON projects (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_articles_published
    ON articles (status, published_at DESC);

CREATE INDEX IF NOT EXISTS idx_profiles_active_created
    ON profiles (is_active DESC, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_resumes_active_created
    ON resumes (is_active DESC, created_at DESC);

-- Revocation keeps the row for auditing; last_seen_at records the latest use.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS revoked_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
