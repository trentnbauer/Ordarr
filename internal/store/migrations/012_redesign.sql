CREATE TABLE preferences (
 key TEXT PRIMARY KEY,
 value TEXT NOT NULL
);
ALTER TABLE subscriptions ADD COLUMN skipped_hash TEXT NOT NULL DEFAULT '';
