-- 000002_create_content_tables.up.sql
-- Creates profiles, articles and resumes with UUID primary keys.
-- Reuses set_updated_at() from migration 000001.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------- profiles
CREATE TABLE IF NOT EXISTS profiles (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(150) NOT NULL,
    headline     VARCHAR(255) NOT NULL DEFAULT '',
    bio          TEXT         NOT NULL DEFAULT '',
    avatar_url   VARCHAR(500) NOT NULL DEFAULT '',
    social_links JSONB        NOT NULL DEFAULT '{}'::jsonb,
    is_active    BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- At most one active profile.
CREATE UNIQUE INDEX IF NOT EXISTS uq_profiles_single_active
    ON profiles (is_active)
    WHERE is_active = TRUE;

CREATE TRIGGER trg_profiles_updated_at
    BEFORE UPDATE ON profiles
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- articles
CREATE TABLE IF NOT EXISTS articles (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         VARCHAR(255) NOT NULL UNIQUE,
    title        VARCHAR(255) NOT NULL,
    excerpt      TEXT         NOT NULL DEFAULT '',
    content      TEXT         NOT NULL DEFAULT '',
    tags         TEXT[]       NOT NULL DEFAULT '{}',
    status       VARCHAR(20)  NOT NULL DEFAULT 'draft'
                 CONSTRAINT chk_articles_status CHECK (status IN ('draft', 'published', 'archived')),
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_articles_slug ON articles USING btree (slug);

CREATE TRIGGER trg_articles_updated_at
    BEFORE UPDATE ON articles
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------- resumes
CREATE TABLE IF NOT EXISTS resumes (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(150) NOT NULL,
    file_url   VARCHAR(500) NOT NULL DEFAULT '',
    content    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    is_active  BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- At most one active resume.
CREATE UNIQUE INDEX IF NOT EXISTS uq_resumes_single_active
    ON resumes (is_active)
    WHERE is_active = TRUE;

CREATE TRIGGER trg_resumes_updated_at
    BEFORE UPDATE ON resumes
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
