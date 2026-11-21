-- A collection's optional cover image: its URL (from the collection file), the downloaded copy
-- Ordarr serves itself, and which cover was last pushed to each Plex playlist as its poster.
ALTER TABLE collections ADD COLUMN cover TEXT NOT NULL DEFAULT '';
CREATE TABLE collection_covers (
 collection_id TEXT PRIMARY KEY REFERENCES collections(id) ON DELETE CASCADE,
 url TEXT NOT NULL,
 content_type TEXT NOT NULL,
 data BLOB NOT NULL,
 sha TEXT NOT NULL,
 fetched_at INTEGER NOT NULL
);
CREATE TABLE destination_cover (
 destination_id TEXT PRIMARY KEY REFERENCES destinations(id) ON DELETE CASCADE,
 sha TEXT NOT NULL
);
