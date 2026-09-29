-- Owner request 2026-09-29: sign in with username or e-mail; admins
-- authorize an account (invite) and the person sets their own password on
-- first access; each user picks which events notify them.
-- pw_hash '' = invited, no password yet. invite_hash is the SHA-256 of the
-- invite code ('' = none); invite_expires is RFC 3339 UTC.
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN invite_hash BLOB;
ALTER TABLE users ADD COLUMN invite_expires TEXT NOT NULL DEFAULT '';
-- JSON of NotifyPrefs ('' = defaults).
ALTER TABLE users ADD COLUMN notify TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_email ON users(email COLLATE NOCASE) WHERE email <> '';
-- remember = 0: "keep me signed in" was off (short session, browser cookie).
ALTER TABLE sessions ADD COLUMN remember INTEGER NOT NULL DEFAULT 1;
