-- A session remembers the zone of the listener it was created on, which the
-- server decides (never a header). A public listener accepts only sessions
-- created on that same public zone, so a cookie from the LAN panel (which can
-- travel in clear text), the tailnet or another public path cannot be
-- replayed from the internet, whatever Host header the client sends. The
-- Host binding of 0012 stays as a second check. '' = a session made before
-- this migration: public listeners refuse it (it still works at home), so
-- outside devices sign in once more.
ALTER TABLE sessions ADD COLUMN zone TEXT NOT NULL DEFAULT '';
