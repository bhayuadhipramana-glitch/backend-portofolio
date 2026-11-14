-- 000002_create_content_tables.down.sql
-- Dropping a table also drops its indexes. The pgcrypto extension is left in
-- place because other objects may depend on it.

DROP TRIGGER IF EXISTS trg_resumes_updated_at ON resumes;
DROP TABLE IF EXISTS resumes;

DROP TRIGGER IF EXISTS trg_articles_updated_at ON articles;
DROP TABLE IF EXISTS articles;

DROP TRIGGER IF EXISTS trg_profiles_updated_at ON profiles;
DROP TABLE IF EXISTS profiles;
