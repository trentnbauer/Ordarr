CREATE TABLE plex_media_pages (
 connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 library_key TEXT NOT NULL,
 payload BLOB NOT NULL,
 connection_revision INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 fetched_at INTEGER NOT NULL,
 attempted_at INTEGER NOT NULL,
 last_error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(connection_id,library_key)
);
