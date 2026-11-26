-- Protected recordings are capped per camera and in total (share of the
-- recordings disk): the cap is checked on every protect, so their sums stay
-- cheap with a small index over the protected rows only.
CREATE INDEX IF NOT EXISTS segments_protected ON segments(camera_id, bytes) WHERE protected=1;
