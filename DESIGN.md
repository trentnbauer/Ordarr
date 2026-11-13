# Ordarr — Product Design

Status: implementation specification (MVP-first)

## Purpose
Ordarr is a self-hosted watch-order manager that mixes movies and individual TV episodes in an exact sequence. It imports, edits, exports, subscribes to and publishes portable watch orders. Missing entries remain in the canonical order and are inserted into published playlists when the destination media server acquires them.

## Core principles
- Ordarr's canonical sequence is the source of truth, not a media-server playlist.
- Never discard, reorder or substitute missing or ambiguously matched entries.
- A destination playlist contains only currently playable, verified items, in canonical order.
- Do not modify or delete original media files.
- All external services are optional except a configured media server for publishing.
- Prefer low idle CPU/RAM usage, small images, minimal dependencies and a single-container deployment.

## Deployment and technical direction
- **Required:** Docker container and Docker Compose example; persistent volume, environment configuration, health check, restart policy, versioned image tags, multi-stage build, and non-root runtime where feasible.
- **Preferred:** Go backend with server-rendered HTML, HTMX and small amounts of JavaScript; embedded static assets; SQLite database in the persistent volume. No separate database, Redis, worker or frontend runtime required.
- Codex may deviate from the preferred stack only if the alternative is materially simpler or more resource-efficient and the rationale is documented.
- Use SQLite migrations, WAL mode where appropriate, graceful shutdown, bounded HTTP clients and configurable background scan intervals.
- Measure and document final image size and idle memory/CPU in a reproducible test environment; do not claim a fixed resource budget without measurement.

## MVP scope (finish before expansion)
1. Single-administrator setup and authenticated management UI.
2. Plex connection via manual server URL/token **and** Plex account sign-in/authorisation, with safe token handling and connection validation.
3. Create, edit, reorder and delete watch orders with movies, episodes, seasons and episode ranges; preserve deliberate repeats.
4. Match external media identifiers against Plex libraries, with manual resolution for ambiguous items.
5. Publish and reconcile exact-order mixed-media Plex **video playlists** (not mixed native collections); respect playlist ownership and permissions.
6. Keep missing items in the canonical order and insert them at the correct position when Plex exposes them.
7. Import/export versioned YAML and JSON definitions; subscribe to URLs/GitHub raw files with per-collection update policy (automatic or approval-required).
8. Show missing media, sync history, errors and destination health.
9. Functional Docker deployment, tests and documentation.

## Data model
- **Collection:** stable ID, title, description, cover (optional), tags, origin (manual/import/subscription/fork), source URL, update policy, source revision, timestamps.
- **Entry:** stable entry ID, collection ID, explicit position, media type (movie/episode), provider identifiers, series/season/episode where relevant, optional display metadata; duplicates are allowed.
- **Destination:** server connection, authorised Plex user, library selection, remote playlist identifier, sync policy, last sync result.
- **Match:** entry-to-server item mapping, confidence/method, availability state, manual override and last verification.
- **Sync run:** start/end, proposed and applied changes, failures, retry state.
- **Request:** optional Sonarr/Radarr request intent, approval/automation policy, deduplication key, outcome.
- **Subscription:** source, polling interval, ETag/Last-Modified when available, last valid document, pending changes and approval state.

## Media identification
- Prefer exact TMDB/TVDB/IMDb or equivalent provider IDs; use provider cross-references only when validated.
- Match episodes by series identifier plus season/episode or reliable episode identifier.
- Fuzzy title/year matching may suggest candidates but must not silently accept uncertain matches.
- Support specials, alternate numbering and multi-episode files via explicit mapping/manual review; do not guess.
- Availability is per destination, not global.

## Collection editing
- Search and add movies, individual episodes, whole seasons and ranges.
- Reorder via drag-and-drop and accessible keyboard controls; support bulk movement and deletion.
- Show the complete canonical order, including unavailable and unreleased entries.
- Allow manual collections, imports, subscriptions and forks. Local edits to subscribed sources require a documented fork or override policy; never silently erase edits.
- Support deliberate repeated entries and preserve their separate sequence positions.

## Portable format
- Accept versioned YAML and JSON with an optional source collection ID, title and ordered `entries`.
- Each item identifies a movie or episode by provider ID, with season/episode as applicable; optional ranges expand deterministically into individual entries.
- Reject invalid versions, malformed items, duplicate collection IDs where inappropriate and untrusted remote URLs safely.
- Export in canonical order; round-trip import/export without losing identifiers or deliberate duplicates.

The implemented version 1 schema uses `id` (optional collection origin), `title`,
`description`, and ordered `entries`. Every entry has a source-stable `id`, `kind`,
`title`, `provider`, and string `external_id`; episodes add `season`/`episode`.
`episode_range` and `season` use explicit `end`, expand deterministically, and export
as individual episodes. Imports always create an independent local collection ID.
See the [portable-format wiki](https://github.com/trentnbauer/Ordarr/wiki/Portable-format)
for examples and limits.

## Plex integration
- Support manual Plex URL/token configuration and Plex account authentication/authorisation. Verify the supported sign-in flow against current Plex APIs; do not implement insecure credential collection.
- Discover accessible libraries and users within granted permissions.
- Publish mixed movies and episodes as ordered **video playlists**. Plex collections are not an equivalent output for arbitrary mixed-media playback.
- Validate remote playlist API capabilities, ordering, item insertion/removal, ownership and pagination with documented API evidence and optional live tests.
- Store the remote playlist ID and only mutate playlists explicitly managed by Ordarr.
- Never overwrite an unrelated user playlist. Handle a missing or deleted managed playlist through an explicit recreate policy.

## Reconciliation and future downloads
1. Load the canonical ordered entries.
2. Resolve each against the destination library; keep missing/unmatched entries in Ordarr.
3. Build the ordered sequence of verified playable destination item IDs, preserving intentional duplicates where supported.
4. Compare with the current managed remote playlist.
5. Apply a minimal safe reconciliation or rebuild only if required by destination API semantics.
6. Record success/failure and retry safely; repeated syncs without changes should be idempotent.

- Trigger sync manually, periodically and from supported library events/webhooks (with periodic reconciliation as fallback).
- Treat content as available only after Plex indexes and exposes the actual playable item.
- If a previously available item disappears, retain the canonical entry and reconcile the playlist.
- Support sync conflict policies: Ordarr authoritative (default for managed playlists), approval required, or paused.
- Preserve last valid state if a source or destination becomes unavailable; avoid destructive updates on partial scan failure.

## External subscriptions
- Accept uploaded files, local imports, direct URLs and GitHub raw collection files.
- Per collection: **automatic apply** or **manual approval** of upstream changes; allow configurable polling interval.
- Show proposed additions, removals and reorderings before approval.
- Use conditional HTTP requests where available and backoff on failure; retain last valid version.
- Validate external URLs and redirects against SSRF risks; limit response size and timeouts.

## Missing media and *arr integration
- MVP: identify missing items and provide clear status; downloads are not required for playlist synchronisation.
- Later phase: connect optional Sonarr and Radarr instances; show monitored, queued, downloading and available states.
- Support manual requests and **optional automatic requesting**, disabled by default and configured explicitly per collection or server.
- Automatic requests require safeguards: only confirmed media identities, duplicate-request suppression, configurable limits, exclusion rules, audit log, and failure handling.
- Do not assume Sonarr supports arbitrary single-episode acquisition in every configuration; validate API capabilities and clearly communicate the request's effect.
- An *arr download is not playable until the media server indexes it.

## User interface
- Dashboard: collections, missing items, pending updates, last sync, connection health.
- Collections: searchable grid/list, status and source filters, create/import/export.
- Detail/editor: complete ordered timeline, episode range picker, drag/keyboard reorder, availability and per-destination status, manual sync, source diff.
- Settings: administrator, Plex, subscriptions, metadata, sync intervals, system/logs; Sonarr/Radarr and Jellyfin settings in later phases.
- Responsive and accessible; no need to build a streaming player.

## Authentication and security
- MVP has one local administrator account; secure first-run setup and session management.
- Plex sign-in authorises a Plex connection and does not automatically replace local administrator authentication unless deliberately designed and documented.
- Protect against CSRF, XSS, SSRF, unsafe file uploads, weak sessions and token exposure; redact secrets from logs.
- Do not store Plex passwords. Encrypt tokens at rest if practical with a documented key-management model; at minimum restrict persistent file permissions and never expose tokens in responses.
- Respect least privilege and protect all write endpoints.

## Post-MVP expansion
1. Sonarr/Radarr status and manual/optional automatic acquisition requests.
2. Jellyfin ordered-playlist integration with separately validated API capabilities and user permissions.
3. Additional destinations such as Emby through an adapter interface.
4. Community catalogue, watch progress, multi-user support and advanced subscriptions.

## Validation and definition of done
- Unit tests for schema validation, range expansion, ordering, matching, duplicates and subscription diffs.
- Integration-style tests with recorded/mocked Plex responses for playlist creation, update, removal, failure and retry.
- Optional live Plex tests using a disposable playlist; clearly identify unverified live behaviours.
- Build, lint/type-check as applicable, test suite and Docker image build must pass.
- Verify persistence after container restart, migration upgrades, health checks and graceful recovery from service outages.
- Each completed TODO must have implementation, appropriate tests, error handling and updated documentation.
- Do not claim real Plex/Jellyfin/*arr compatibility without corresponding live verification.

## Additional integration and data-safety decisions

- **Playlist ownership:** During the MVP, publish Plex playlists under the connected administrator's authorised Plex account. Support for choosing other users is a later enhancement and must respect Plex permissions.
- **Separate authentication:** Ordarr uses a local administrator login. Plex account sign-in is a connection-authorisation method, not a replacement for Ordarr authentication. Manual Plex URL/token configuration remains supported.
- **Automatic acquisition:** Sonarr/Radarr automatic requests are opt-in and disabled by default. When implemented, enforce configurable request limits, deduplication, exclusions and an audit trail; support manual requests as well.
- **Playlist safety:** Only modify playlists Ordarr created or playlists explicitly assigned to its management. Validate a proposed replacement before applying it; failed or partial synchronisations must not intentionally clear or truncate a previously valid playlist. Record failures and permit safe retries.
- **Sync recovery:** Persist each managed destination playlist's stable identifier and last successfully published ordered sequence. Reconcile against the actual destination state and recover from interrupted synchronisations without unintended duplicates.
- **Verification:** Compilation and mocked API tests alone do not establish real Plex compatibility. Use production API implementations, test against documented API behaviour, and record which behaviours have and have not been tested against a real Plex server. Do not label unverified integrations as validated.
