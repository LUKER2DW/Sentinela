-- What the recorder reports to the API: it cannot write the audit log,
-- which lives in the private database. The API announces each row to the
-- admins and copies it into the audit log. Today: protected clips deleted
-- by the full-disk valve.
CREATE TABLE IF NOT EXISTS recorder_notices(
  id INTEGER PRIMARY KEY,
  at TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
