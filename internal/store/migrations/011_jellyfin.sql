CREATE TABLE jellyfin_connections (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 base_url TEXT NOT NULL,
 token_cipher BLOB NOT NULL,
 allow_http INTEGER NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 snapshot BLOB NOT NULL,
 checked_at INTEGER NOT NULL
);
CREATE TABLE jellyfin_catalogues (
 connection_id TEXT PRIMARY KEY REFERENCES jellyfin_connections(id) ON DELETE CASCADE,
 connection_revision INTEGER NOT NULL,
 generation TEXT NOT NULL,
 scanned_at INTEGER NOT NULL
);
CREATE TABLE jellyfin_catalogue_items (
 connection_id TEXT NOT NULL REFERENCES jellyfin_connections(id) ON DELETE CASCADE,
 generation TEXT NOT NULL,
 item_id TEXT NOT NULL,
 payload BLOB NOT NULL,
 PRIMARY KEY(connection_id,generation,item_id)
);
CREATE TABLE jellyfin_manual_matches (
 connection_id TEXT NOT NULL REFERENCES jellyfin_connections(id) ON DELETE CASCADE,
 entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
 identity TEXT NOT NULL,
 item_id TEXT NOT NULL,
 PRIMARY KEY(connection_id,entry_id)
);
