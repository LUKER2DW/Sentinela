-- Web Push subscriptions are bound to the session that created them, like
-- fcm_tokens (0009): when the session ends (logout, revoke, expiry, password
-- change, disable) the subscription goes with it. NULL = a subscription made
-- before this migration; it keeps working until the account's sessions are
-- dropped (password change, disable, new invite) or the SPA subscribes again.
ALTER TABLE push_subscriptions ADD COLUMN session_hash BLOB REFERENCES sessions(id_hash) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS push_subscriptions_session ON push_subscriptions(session_hash);
CREATE INDEX IF NOT EXISTS push_subscriptions_user ON push_subscriptions(user_id);

-- LastLogin and the per-user audit list filter by username (and action).
CREATE INDEX IF NOT EXISTS audit_user_action ON audit(username, action, id);
