CREATE TABLE collections (
 id TEXT PRIMARY KEY,
 title TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 forked_from TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision > 0),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE entries (
 id TEXT PRIMARY KEY,
 collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('movie','episode')),
 title TEXT NOT NULL,
 provider TEXT NOT NULL CHECK(provider IN ('tmdb','tvdb','imdb')),
 external_id TEXT NOT NULL,
 season INTEGER NOT NULL CHECK(season >= 0),
 episode INTEGER NOT NULL CHECK(episode >= 0),
 UNIQUE(collection_id, position)
);
