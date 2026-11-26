-- The private database: the API's tables, opened by `sentinela serve` only,
-- in a folder the recorder container does not mount. The schema is the one
-- the shared index reached at 0013; releases before the split kept these
-- tables there and the API moves their rows here once (store.moveLegacy).
-- Later changes to these tables go in this folder, never in migrations/.
CREATE TABLE users(
  id INTEGER PRIMARY KEY,
  username TEXT UNIQUE NOT NULL COLLATE NOCASE,
  pw_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('admin','operator','viewer')),
  created_at TEXT NOT NULL,
  cameras TEXT NOT NULL DEFAULT '',
  totp_secret TEXT NOT NULL DEFAULT '',
  totp_step INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  email TEXT NOT NULL DEFAULT '',
  invite_hash BLOB,
  invite_expires TEXT NOT NULL DEFAULT '',
  notify TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX users_email ON users(email COLLATE NOCASE) WHERE email <> '';

CREATE TABLE sessions(
  id_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  mfa INTEGER NOT NULL DEFAULT 0,
  remember INTEGER NOT NULL DEFAULT 1,
  host TEXT NOT NULL DEFAULT '',
  zone TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE review_seen(
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL,
  seen_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_id)
) WITHOUT ROWID;

CREATE TABLE push_subscriptions(
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint TEXT UNIQUE NOT NULL,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_ok TEXT,
  session_hash BLOB REFERENCES sessions(id_hash) ON DELETE CASCADE
);
CREATE INDEX push_subscriptions_session ON push_subscriptions(session_hash);
CREATE INDEX push_subscriptions_user ON push_subscriptions(user_id);

CREATE TABLE fcm_tokens(
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_hash BLOB NOT NULL REFERENCES sessions(id_hash) ON DELETE CASCADE,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_ok TEXT NOT NULL DEFAULT ''
);
CREATE INDEX fcm_tokens_user ON fcm_tokens(user_id);

-- Small key/value settings changed from the UI (notification mode, detection
-- zones, onboarding, ONVIF pull points).
CREATE TABLE settings(
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE audit(
  id INTEGER PRIMARY KEY,
  at TEXT NOT NULL,
  user_id INTEGER,
  username TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  ok INTEGER NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_at ON audit(at);
CREATE INDEX audit_user_action ON audit(username, action, id);
