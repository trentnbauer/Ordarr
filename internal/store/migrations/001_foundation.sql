CREATE TABLE administrator (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, password_hash BLOB NOT NULL);
CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, administrator_id INTEGER NOT NULL REFERENCES administrator(id));
CREATE INDEX sessions_expiry ON sessions(expires_at);
