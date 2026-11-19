-- A session is bound to the Host it was created on (host and port as the
-- browser sent them). Browsers do not isolate cookies by port, so a service
-- on another port of the same machine could otherwise replay the cookie at a
-- public listener. '' = a session made before this migration; it keeps
-- working everywhere until it ends.
ALTER TABLE sessions ADD COLUMN host TEXT NOT NULL DEFAULT '';
