-- 003_settings.sql — runtime-editable settings, read by the admin UI and the
-- LLM router. Run as a role that can GRANT (not the `quicky` app role).
BEGIN;

CREATE TABLE settings (
  key         TEXT PRIMARY KEY,
  value       TEXT NOT NULL,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
GRANT SELECT, INSERT, UPDATE ON settings TO quicky;

COMMIT;
