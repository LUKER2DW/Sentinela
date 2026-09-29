-- Web Push subscriptions (ADR-0013), one per browser/device.
CREATE TABLE push_subscriptions(
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint TEXT UNIQUE NOT NULL,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_ok TEXT
);

-- Small key/value settings changed from the UI (e.g. notification mode).
CREATE TABLE settings(
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
