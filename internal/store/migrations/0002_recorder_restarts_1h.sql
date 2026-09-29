-- Recorder reconnects in the last hour, to spot reconnect storms (PLAN Phase 9 Wave 1).
ALTER TABLE recorder_status ADD COLUMN restarts_1h INTEGER NOT NULL DEFAULT 0;
