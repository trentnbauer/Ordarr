CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 target TEXT NOT NULL,
 state TEXT NOT NULL,
 progress INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX jobs_state ON jobs(state,created_at);
CREATE UNIQUE INDEX active_job ON jobs(kind,target) WHERE state IN ('queued','running');
CREATE TABLE plex_catalogues (
 connection_id TEXT PRIMARY KEY REFERENCES plex_connections(id) ON DELETE CASCADE,
 connection_revision INTEGER NOT NULL,
 generation TEXT NOT NULL,
 scanned_at INTEGER NOT NULL,
 last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE plex_catalogue_items (
 connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 generation TEXT NOT NULL,
 rating_key TEXT NOT NULL,
 payload BLOB NOT NULL,
 PRIMARY KEY(connection_id,generation,rating_key)
);
CREATE TABLE manual_matches (
 connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
 identity TEXT NOT NULL,
 rating_key TEXT NOT NULL,
 PRIMARY KEY(connection_id,entry_id)
);
