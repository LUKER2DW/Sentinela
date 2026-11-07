-- Per-camera permissions (PLAN Phase 9, Wave 4). Comma-separated camera ids
-- a non-admin user may see; '' means every camera. Admins always see all.
ALTER TABLE users ADD COLUMN cameras TEXT NOT NULL DEFAULT '';
