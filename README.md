# Ordarr

Self-hosted watch-order management for movies and TV episodes.

**Current milestone: canonical collection editor.** Create mixed movie/episode watch
orders, edit entries, expand explicit seasons/ranges, preserve repeats, reorder with
a keyboard or drag handle, move/remove selected entries, and make independent copies.
Administrator authentication and SQLite persistence are included. Plex connections,
playlist sync and import/export remain on the [development checklist](TODO.md).
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

The locally built image tag is `ordarr:0.2.0-dev`; no registry release is published.

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
plus logout, revoked-cookie rejection, login, and clean shutdown. Go tests cover migration rollback, future-schema rejection,
concurrent administrator creation, authentication expiry, input limits, CSRF, escaping,
security flags, rate limiting and health failures. Collection tests additionally cover
range/season expansion, stable IDs, fork independence, optimistic conflicts, forced-write
rollback, pagination, limits, and upgrading an existing foundation database.

Optional browser checks require Playwright installed outside application dependencies:
`node scripts/browser-smoke.cjs`. Set `CHROMIUM_PATH` to a system Chromium executable,
or let Playwright use its downloaded browser. CI runs the Go checks and Compose smoke;
the workflow itself has not been exercised on GitHub yet.

In a managed cloud build requiring a proxy CA:

```sh
docker build --secret id=proxy_ca,src="$CODEX_PROXY_CERT" -t ordarr:0.2.0-dev .
```

The optional BuildKit secret supplies trust during dependency download without placing
session certificates into image layers. TLS and Go module checksum verification stay enabled.

## Backups and migrations

Stop Ordarr before making a filesystem backup of the named volume (including database,
WAL, and shared-memory files). Back up to a separate protected location, restart, and
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
