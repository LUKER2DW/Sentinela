-- Admins can switch an account off without deleting it (owner request
-- 2026-09-29): a disabled user cannot sign in and loses every session.
ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
