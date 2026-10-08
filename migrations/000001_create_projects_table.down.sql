-- 000001_create_projects_table.down.sql
-- Rolls back the projects table and its associated trigger/function.

DROP TRIGGER IF EXISTS trg_projects_updated_at ON projects;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS projects;
