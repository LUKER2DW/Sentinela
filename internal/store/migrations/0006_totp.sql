-- Two-step login for the public access paths (ADR-0019). totp_secret is
-- base32 ('' = 2FA off); totp_step is the last accepted RFC 6238 step, so a
-- code works once. sessions.mfa marks sessions created with 2FA: only those
-- are honoured in public zones.
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN mfa INTEGER NOT NULL DEFAULT 0;
