-- Every camera id ever configured. An id is never given to a second
-- camera: grants, recordings, events and settings keyed by it stay with the
-- camera that had it. removed_at is set once the id is no longer
-- configured (the API fills this table when it starts); an id configured
-- again by hand moves the old camera's data to an archive id first.
CREATE TABLE IF NOT EXISTS camera_ids(
  id TEXT PRIMARY KEY,
  added_at TEXT NOT NULL,
  removed_at TEXT
);
