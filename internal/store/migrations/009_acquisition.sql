CREATE TABLE arr_connections (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('sonarr','radarr')),
 name TEXT NOT NULL,
 base_url TEXT NOT NULL,
 key_cipher TEXT NOT NULL,
 profile_id INTEGER NOT NULL,
 root_id INTEGER NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 snapshot BLOB NOT NULL,
 checked_at INTEGER NOT NULL,
 automatic INTEGER NOT NULL DEFAULT 0 CHECK(automatic IN (0,1)),
 require_approval INTEGER NOT NULL DEFAULT 1 CHECK(require_approval IN (0,1)),
 daily_limit INTEGER NOT NULL DEFAULT 5 CHECK(daily_limit BETWEEN 1 AND 100),
 exclusions TEXT NOT NULL DEFAULT '',
 next_run INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE arr_automation (
 connection_id TEXT NOT NULL REFERENCES arr_connections(id) ON DELETE CASCADE,
 collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 plex_connection_id TEXT NOT NULL REFERENCES plex_connections(id) ON DELETE CASCADE,
 PRIMARY KEY(connection_id,collection_id)
);
CREATE TABLE acquisition_requests (
 id TEXT PRIMARY KEY,
 connection_id TEXT NOT NULL REFERENCES arr_connections(id) ON DELETE CASCADE,
 provider_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 state TEXT NOT NULL,
 origin TEXT NOT NULL,
 connection_revision INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 attempted_at INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 UNIQUE(connection_id,provider_id)
);
CREATE TABLE acquisition_attempts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 request_id TEXT NOT NULL REFERENCES acquisition_requests(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL REFERENCES arr_connections(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL
);
