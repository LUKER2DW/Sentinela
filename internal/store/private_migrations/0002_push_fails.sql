-- Failed deliveries in a row of a Web Push subscription (reset by a good
-- one). A subscription that never delivered and failed three times in a row
-- is dropped as a dead endpoint (SECURITY.md rule 12).
ALTER TABLE push_subscriptions ADD COLUMN fails INTEGER NOT NULL DEFAULT 0;
