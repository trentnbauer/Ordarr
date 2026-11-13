CREATE TABLE plex_accounts (
 id INTEGER PRIMARY KEY CHECK(id=1),
 user_id INTEGER NOT NULL,
 username TEXT NOT NULL,
 friendly_name TEXT NOT NULL,
 restricted INTEGER NOT NULL,
 client_id TEXT NOT NULL,
 credentials_cipher BLOB NOT NULL,
 expires_at INTEGER NOT NULL,
 checked_at INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0)
);
CREATE TABLE plex_auth_attempts (
 id TEXT PRIMARY KEY,
 session_hash TEXT NOT NULL,
 private_cipher BLOB NOT NULL,
 client_id TEXT NOT NULL,
 kid TEXT NOT NULL,
 pin_id INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 next_poll_at INTEGER NOT NULL DEFAULT 0,
 account_revision INTEGER NOT NULL
);
ALTER TABLE plex_connections ADD COLUMN account_id INTEGER REFERENCES plex_accounts(id);
ALTER TABLE plex_connections ADD COLUMN resource_id TEXT NOT NULL DEFAULT '';
