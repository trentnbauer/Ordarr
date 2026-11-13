CREATE TABLE plex_connections (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 base_url TEXT NOT NULL,
 client_id TEXT NOT NULL,
 token_cipher BLOB NOT NULL,
 machine_id TEXT NOT NULL,
 server_name TEXT NOT NULL,
 server_version TEXT NOT NULL,
 allow_http INTEGER NOT NULL CHECK(allow_http IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 checked_at INTEGER NOT NULL,
 attempted_at INTEGER NOT NULL,
 last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE plex_libraries (
 connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 library_key TEXT NOT NULL,
 title TEXT NOT NULL,
 kind TEXT NOT NULL,
 PRIMARY KEY(connection_id,library_key)
);
