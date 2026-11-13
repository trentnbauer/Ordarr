# Foundation decisions

- Use Go, server-rendered `html/template` forms, embedded CSS, and SQLite. HTMX is
  unnecessary for setup and authentication and can be introduced when collection
  editing needs progressive enhancement. There is no frontend build dependency.
- Use `mattn/go-sqlite3` with CGO and bundled SQLite. This adds a C build requirement
  but uses the established SQLite driver. A Debian builder/runtime pair supplies
  compatible libc without a separate database service. Reconsider pure-Go SQLite
  only with measured deployment benefits.
- Keep migrations embedded and transactional, enable foreign keys, WAL journaling,
  full synchronous writes, a five-second busy timeout and one database connection.
  Database files are mode 0600 and newly created data directories are 0700.
- The administrator table has a database-enforced singleton key. Concurrent setup
  requests cannot create a second administrator. First-run setup must happen on a
  trusted connection before allowing others to reach the instance.
- Passwords use bcrypt's default cost and enforce its 72-byte input limit. Sessions
  use 256-bit random opaque tokens, store only SHA-256 hashes, expire absolutely
  after 24 hours and are explicitly revoked. Cookie sessions are HttpOnly and
  SameSite=Strict, with Secure enabled by explicit HTTPS deployment configuration.
- Require same-origin POSTs when an Origin header is present, and a random double
  submit CSRF cookie/form token on every POST, including login and setup. Requests
  have bounded form bodies and HTTP timeouts. No credential values enter logs.
- Settings currently displays actual instance/account state. There are no fake
  media connections or inactive collection controls. Plex authorisation will remain
  separate from the local administrator login.
- Keep automatic acquisition and all post-MVP integrations out of this milestone.
- Licensing remains reserved to the repository owner: no open-source licence is
  added or implied. An owner-selected licence is an open release decision.

## Still open

Password change/recovery, bootstrap provisioning beyond trusted local setup, public
release licensing, Plex sign-in implementation/live verification, external key management/rotation,
provider metadata lookup, and live Plex test access belong to subsequent work. No
Plex credentials are required for the foundation. Follow TODO.md in phase order.

## Canonical collection editor

- Migration 002 adds collections and stable ordered entries without touching existing
  administrator/session rows. Entries have individual random IDs; provider IDs are
  deliberately non-unique so repeated media remains separate. Positions are unique
  within each collection and are updated transactionally without deleting retained rows.
- Manual media identity is provider + movie ID or provider + series ID + season/episode.
  Labels are editable and never used to guess matches. Availability remains unmatched
  until a real destination adapter verifies it. Season counts are explicit, and ranges
  expand deterministically. Alternate numbering requires explicit user input.
- Every collection mutation requires its current revision. SQLite serialises the
  transaction and rejects stale edits with HTTP 409. Forking also checks the revision.
  A fork is an independent local copy with new entry IDs, not a subscription; source
  deletion retains its provenance string. Import/subscription policy follows in Phase 5.
- Bulk moves preserve canonical relative order among selected entries. The target
  position is interpreted after removing those entries. Deletion is explicit.
- Ordinary forms and Up/Down ordering work without JavaScript. Native drag-and-drop
  uses a visible drop marker and submits the complete sequence; the server rejects
  missing, duplicate or foreign entry IDs. Cancelled drags never mutate the sequence.
  Keyboard moves restore focus to the moved entry. A reserved live-status area avoids
  shifting drop targets when dragging begins.
- Bound collections to 1,000 entries and one expansion to 200 episodes. Collection
  forms have a bounded 128 KiB body (authentication remains 8 KiB). Search uses literal
  SQLite matching and a 20-item page; provider lookup is deferred to real adapters.
- When destination matching is added, editing media identity must invalidate its old
  match; changing labels or positions must retain the same canonical entry identity.

## Manual Plex connectivity

- Migration 003 stores independent named server connections and the last complete
  library snapshot. Connection/token updates and library replacement commit together;
  failed rechecks keep old data with a recorded failure. Revisions reject stale writes.
- The real adapter is read-only. It verifies root metadata and accessible libraries,
  pins the machine identifier, preserves platform proxies/TLS and never follows
  redirects or arbitrary response URLs. Private/loopback destinations are required
  for self-hosted Plex; link-local/metadata destinations are rejected. Proxy-side DNS
  and destination access remain subject to the proxy's controls.
- A token is sent only in its configured server's header. Blank token retention is
  allowed only at the same URL; changing that address requires explicit credentials.
  HTTPS remains verified, and plaintext HTTP requires explicit operator consent.
- AES-256-GCM ciphertext uses a fresh nonce and AAD bound to the connection ID. The
  atomically generated 0600 key lives at `<data directory>/credentials.key`, separately
  from SQLite in the same persistent volume. Database-only exposure does not reveal
  tokens; host/whole-volume compromise remains outside this protection. Backups must
  retain both. Missing keys with existing credentials fail startup rather than replacing
  them. Key rotation is not implemented. Standard Linux hard links and directory fsync
  are required for atomic first-run key publication.
- Successful server reads do not establish Plex account identity, ownership or write
  permissions. No user/owner identity is inferred from server-owner metadata. Account
  sign-in, user discovery, media matching and publishing remain separate milestones.
- API assumptions and remaining live checks are recorded in `plex-api.md`. Production
  never substitutes a fixture service; only tests run disposable HTTP fixtures.
