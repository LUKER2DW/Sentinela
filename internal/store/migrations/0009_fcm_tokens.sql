-- ADR-0020: Firebase Cloud Messaging tokens of the Android app, per user.
-- The token is the device's FCM registration; it carries no event data.
CREATE TABLE fcm_tokens(
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_hash BLOB NOT NULL REFERENCES sessions(id_hash) ON DELETE CASCADE,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_ok TEXT NOT NULL DEFAULT ''
);
CREATE INDEX fcm_tokens_user ON fcm_tokens(user_id);
