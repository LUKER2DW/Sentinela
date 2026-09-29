-- Review items a user has seen (item id = first event id, internal/review).
CREATE TABLE review_seen(
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL,
  seen_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_id)
) WITHOUT ROWID;
