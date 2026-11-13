CREATE TABLE subscriptions (
 id TEXT PRIMARY KEY,
 collection_id TEXT NOT NULL UNIQUE REFERENCES collections(id) ON DELETE CASCADE,
 url TEXT NOT NULL,
 policy TEXT NOT NULL CHECK(policy IN ('automatic','approval','paused')),
 revision INTEGER NOT NULL DEFAULT 1,
 etag TEXT NOT NULL DEFAULT '',
 modified TEXT NOT NULL DEFAULT '',
 last_valid BLOB,
 pending BLOB,
 next_run INTEGER NOT NULL DEFAULT 0,
 failures INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 checked_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE source_entries (
 subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
 source_id TEXT NOT NULL,
 entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
 PRIMARY KEY(subscription_id,source_id)
);
