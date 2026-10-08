-- 000001_create_projects_table.up.sql
-- Creates the core projects table with all required columns.

CREATE TABLE IF NOT EXISTS projects (
    id          SERIAL PRIMARY KEY,
    title       VARCHAR(150) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    tech_stack  TEXT[]       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Automatically set updated_at on every UPDATE.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_projects_updated_at
    BEFORE UPDATE ON projects
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
