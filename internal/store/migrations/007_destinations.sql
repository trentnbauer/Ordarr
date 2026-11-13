CREATE TABLE destinations (
 id TEXT PRIMARY KEY,
 collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 title TEXT NOT NULL,
 remote_id TEXT NOT NULL DEFAULT '',
 user_id INTEGER NOT NULL DEFAULT 0,
 policy TEXT NOT NULL CHECK(policy IN ('authoritative','approval','paused')),
 interval_minutes INTEGER NOT NULL DEFAULT 0 CHECK(interval_minutes>=0),
 next_run INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL DEFAULT 1,
 last_sequence BLOB NOT NULL DEFAULT '[]',
 last_collection_revision INTEGER NOT NULL DEFAULT 0,
 last_success INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE sync_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 destination_id TEXT NOT NULL REFERENCES destinations(id) ON DELETE CASCADE,
 job_id TEXT NOT NULL,
 state TEXT NOT NULL,
 proposed BLOB NOT NULL,
 message TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
