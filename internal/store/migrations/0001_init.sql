CREATE TABLE users(
  id INTEGER PRIMARY KEY,
  username TEXT UNIQUE NOT NULL COLLATE NOCASE,
  pw_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('admin','operator','viewer')),
  created_at TEXT NOT NULL
);

CREATE TABLE sessions(
  id_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

-- camera_id is the config id (e.g. "cam1"); cameras live in config.yaml.
CREATE TABLE segments(
  id INTEGER PRIMARY KEY,
  camera_id TEXT NOT NULL,
  start_utc TEXT NOT NULL,
  end_utc TEXT NOT NULL,
  path TEXT UNIQUE NOT NULL,
  bytes INTEGER NOT NULL,
  protected INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX segments_cam_start ON segments(camera_id, start_utc);
CREATE INDEX segments_start ON segments(start_utc);

CREATE TABLE events(
  id INTEGER PRIMARY KEY,
  camera_id TEXT NOT NULL,
  type TEXT NOT NULL,
  start_utc TEXT NOT NULL,
  end_utc TEXT,
  meta TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_cam_start ON events(camera_id, start_utc);

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

-- Written by `sentinela record` (ADR-0007), read by the API.
CREATE TABLE recorder_status(
  camera_id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  since_utc TEXT NOT NULL,
  heartbeat_utc TEXT NOT NULL,
  restarts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  current_file TEXT NOT NULL DEFAULT ''
);
