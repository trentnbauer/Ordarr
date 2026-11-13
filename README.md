# Ordarr

Self-hosted watch-order management for movies and TV episodes.

**Current milestone: manual Plex connectivity plus canonical collection editing.** Create mixed movie/episode watch
orders, edit entries, expand explicit seasons/ranges, preserve repeats, reorder with
a keyboard or drag handle, move/remove selected entries, and make independent copies.
Administrator authentication and SQLite persistence are included. Manual Plex URL/token
setup now validates read access and discovers accessible libraries. Account sign-in,
media matching, playlist sync and import/export remain on the [development checklist](TODO.md).
No live media-server compatibility is claimed.

## Run with Docker Compose

```sh
cp .env.example .env
docker compose up -d --build --wait
```

Open your host's port 8080 and create the first administrator. The default publication
is loopback only (`127.0.0.1`). For a remote host, use an SSH tunnel during first-run
setup. The first successful setup creates the sole administrator; setup then closes.
Use a unique password of 12–72 **bytes** (bcrypt's input limit).

Data lives in the `ordarr-data` named volume at `/data/ordarr.db`. Container replacement
preserves it. `docker compose down` preserves it; **`docker compose down -v` deletes it**.
The runtime runs as UID/GID 10001, drops capabilities, and has a read-only root filesystem.
Bind-mounted data directories must be writable by that user.

```sh
docker compose logs --tail=100 ordarr
docker compose exec ordarr /usr/local/bin/ordarr healthcheck
docker compose down
```

The locally built image tag is `ordarr:0.3.0-dev`; no registry release is published.

## Create a watch order

After signing in, choose **Create watch order**. Give it a title and description,
then add media using its known TMDB, TVDB or IMDb ID and a display title. For episodes,
use the **series** provider ID plus season/episode numbers. Season 0 represents
specials. These are explicit user-supplied identities, not verified metadata matches.

- Add a movie, single episode, inclusive episode range, or whole season with an
  explicit episode count. Whole seasons expand episodes 1 through that count; the
  application never guesses a season length. Each addition expands at most 200
  episodes; a collection holds at most 1,000 entries.
- Adding existing media again creates a separate entry. Every occurrence has a stable
  ID. Editing/reordering retains that ID; duplication creates new IDs in an independent
  local copy, with the source collection ID retained as provenance.
- Use **Up/Down** by keyboard, drag a handle, or select entries for a bulk move. A bulk
  move preserves selected entries' original relative order and inserts them at the
  requested final position after removing them from their previous positions.
- Entry removal requires confirmation. Collection deletion requires typing its title;
  it never deletes source media or independent copies.
- Search collection titles/descriptions and filter by movie, episode, or local copy.
  Lists paginate in groups of 20. SQLite's built-in case-insensitive search folds
  ASCII case; non-ASCII search is literal.

The canonical sequence includes all entries, including repeats and items not yet
matched to any media server. **Unmatched does not mean unavailable:** no library or
release-status check has happened yet. Nothing is published to Plex at this stage.

Every mutation checks the collection revision inside a transaction. Stale tabs receive
HTTP 409 rather than overwriting newer changes. Review the current collection before
trying again. Ordering writes are atomic, retain existing entry rows, and roll back on
failure. Standard forms work without JavaScript; drag ordering is an enhancement.

## Connect Plex manually

Open **Settings → Manage Plex connections** after signing in. Enter a connection name,
a server origin such as `https://plex.example:32400`, and a token authorised to read
that server. Do not put credentials in the URL. The URL is resolved from Ordarr's
machine/container, so `localhost` inside Docker refers to the Ordarr container, not
Plex on your Docker host. Use a reachable LAN address or hostname when appropriate.

Ordarr validates the server's machine ID and reads its accessible library sections
before saving. Tokens are sent only in `X-Plex-Token` headers, never query parameters.
HTTPS verification stays enabled, redirects are refused, and a different server
machine ID cannot silently replace an existing destination. Private-network and
loopback Plex addresses are supported; link-local and known metadata addresses are
blocked. Direct connections check DNS results at dial time. When using an inherited
proxy, destination resolution/access control also depends on that proxy's policy.

HTTP requires explicitly allowing plaintext transport to a trusted server. It sends
the token without encryption; prefer HTTPS. Custom CA trust should be installed or
mounted through the deployment's supported trust configuration, never by disabling
certificate verification.

Use **Recheck** to refresh the library snapshot. Failed, partial or inconsistent
responses preserve the last good snapshot and show it as potentially stale. Token
replacement is validated before committing. A blank replacement keeps the current
token at the same URL; changing the URL requires supplying a token explicitly. Up
to ten connections and 1,000 discovered libraries per connection are supported.

**This checks connectivity and library read access only.** A server may allow read
access from a trusted network even with an invalid token. The check does not prove
Plex account identity, ownership or playlist write permission. Account sign-in, user
selection, media scans/matching and publishing are not implemented yet. No Plex
media, users, server settings or playlists are modified by this milestone. Removing
an Ordarr connection is local only; revoke an exposed token separately through Plex.
Manual tokens may expire or be revoked; follow [Plex's token guidance](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).

### Credential storage and backups

Tokens are AES-256-GCM encrypted in SQLite with fresh nonces and connection-bound
associated data. The 32-byte `credentials.key` is generated atomically in the persistent
data directory, mode 0600. It must be backed up **with the matching database** and
kept private. Startup refuses to replace a missing key when saved credentials exist,
or to use a corrupt, symlinked or group/world-accessible key file. There is no automatic
key rotation/recovery yet. Restore the original key and database rather than inventing
a replacement key.

This protects a database-only disclosure; it does not protect against someone who can
read both the key and database or control the host. The key is in the same named volume
for reliable self-hosted persistence. Back up the whole stopped volume, including key,
database and any SQLite sidecars. Restoring a backup can also restore then-valid
Ordarr sessions and Plex tokens; protect backups and revoke credentials when needed.
The application never returns tokens in rendered forms and uses fixed external-error
messages instead of logging request headers, response bodies or credential values.

## Develop

Requires Go 1.25 or newer, a C compiler (CGO SQLite), and Make. No Node, Redis, or
external database is required to run or build the application.

```sh
go mod download
make check test build
./bin/ordarr
```

Native defaults: `127.0.0.1:8080`, database `./data/ordarr.db`.
The cloud toolchain is activated with:

```sh
export PATH=/workspace/.ordarr-tools/go1.27.4/bin:$PATH
export GOPATH=/workspace/.ordarr-tools/gopath
export GOCACHE=/workspace/.ordarr-tools/go-build
```

| Variable | Native default | Purpose |
| --- | --- | --- |
| `ORDARR_ADDR` | `127.0.0.1:8080` | HTTP listen address; Compose uses `0.0.0.0:8080` inside container |
| `ORDARR_DATA_DIR` | `./data` | Persistent SQLite directory; Compose uses `/data` |
| `ORDARR_SECURE_COOKIES` | `false` | Set `true` for access through HTTPS |
| `ORDARR_BIND_IP` | `127.0.0.1` | Compose host publication address |
| `ORDARR_PORT` | `8080` | Compose host port |
| `TZ` | `Etc/UTC` in Compose | Runtime time zone |

Before exposing beyond a trusted local connection, put Ordarr behind an HTTPS reverse
proxy and enable secure cookies. Preserve the original `Host` header. Ordarr does not
trust `X-Forwarded-*` headers; secure-cookie configuration explicitly indicates HTTPS
for form-origin checks. There is no public registration, password recovery, or password
change UI yet. Keep credentials in a password manager. Losing the password currently
requires restoring a known usable backup or a separate recovery implementation; do not
remove the database to reset authentication on an instance with data you need.

Sessions expire after 24 hours, are hashed in SQLite and revoked on logout. At most 20
sessions are retained. Authentication POSTs are limited to five per source IP per minute;
behind a reverse proxy clients share the proxy's limit. Limits reset on restart. Logs
contain fixed lifecycle/error events, never request bodies, credentials, or cookies.

## Validation

```sh
make check test build
python3 scripts/smoke.py
# Build image first; uses a disposable Compose project and deletes only its own volume:
python3 scripts/smoke.py --compose
```

The smoke test verifies setup, authenticated settings, and persistence of administrator,
session, mixed collection order and repeated entries after process/container replacement,
plus private credential-key persistence/permissions, logout, revoked-cookie rejection,
login, and clean shutdown. Go tests cover migration rollback, future-schema rejection,
concurrent administrator creation, authentication expiry, input limits, CSRF, escaping,
security flags, rate limiting and health failures. Collection tests additionally cover
range/season expansion, stable IDs, fork independence, optimistic conflicts, forced-write
rollback, pagination, limits, and upgrading an existing foundation database.

Optional browser checks require Playwright installed outside application dependencies:
`node scripts/browser-smoke.cjs`. The additional
`node scripts/plex-browser-smoke.cjs` exercises real application requests against a
disposable local Plex-shaped fixture, including encrypted-token restart and failed
recheck retention. This is not live Plex verification. Set `CHROMIUM_PATH` to a system Chromium executable,
or let Playwright use its downloaded browser. CI runs the Go checks and Compose smoke;
the workflow itself has not been exercised on GitHub yet.

In a managed cloud build requiring a proxy CA:

```sh
docker build --secret id=proxy_ca,src="$CODEX_PROXY_CERT" -t ordarr:0.3.0-dev .
```

The optional BuildKit secret supplies trust during dependency download without placing
session certificates into image layers. TLS and Go module checksum verification stay enabled.

## Backups and migrations

Stop Ordarr before making a filesystem backup of the named volume (including database,
WAL, shared-memory files, and `credentials.key`). Back up to a separate protected location, restart, and
verify health. Restore into an empty volume owned by UID 10001 with the service stopped.
A consistent SQLite backup API can be added later for online backups; copying a live
SQLite database file alone is unsafe.

Migrations run transactionally on startup and record `PRAGMA user_version`. Newer,
unsupported schema versions fail startup rather than attempting a downgrade. Back up
before upgrading. Do not run different application versions against the same volume.

See the validation notes for measured foundation resources, current collection-editor
checks, and nested-cgroup limitations. Re-measure under your actual media workload.

See [architecture decisions](docs/decisions.md), [validation evidence](docs/validation.md),
and [contribution guidance](CONTRIBUTING.md).
