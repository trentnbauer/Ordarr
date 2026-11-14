# Ordarr — Development Checklist

Check items only when implemented and validated. Complete the **Plex MVP** before starting post-MVP phases. Record live integration tests separately from mocked tests.

## Phase 0 — Repository and decisions
- [x] Review `DESIGN.md`, `TODO.md`, and `AGENTS.md`; document assumptions and open decisions.
- [x] Confirm Go + lightweight HTML/HTMX + SQLite or document a justified alternative.
- [x] Establish project layout, formatting, linting and CI.
- [ ] Add README, example environment configuration, licensing decision and contribution guidance.

## Phase 1 — Deployable foundation (MVP)
- [x] Implement application server and embedded frontend assets.
- [x] Create SQLite schema, migrations and persistent storage configuration.
- [x] Implement first-run single-admin setup, login/logout and secure sessions.
- [x] Build responsive application shell and settings pages.
- [x] Add structured, secret-redacted logs, health endpoint and graceful shutdown.
- [x] Create multi-stage Dockerfile and minimal runtime image.
- [x] Create working Docker Compose example with volume, healthcheck, restart policy and configurable ports/time zone.
- [x] Add startup, persistence and migration tests.

## Phase 2 — Collection model and editor (MVP)
- [x] Implement collections and stable ordered entries.
- [x] Support movies, individual episodes, season expansion and episode ranges.
- [x] Preserve intentional repeated entries.
- [x] Implement create, edit, duplicate/fork, delete and reorder.
- [x] Add accessible drag-and-drop and keyboard ordering.
- [x] Build collection list/detail/editor and search/filter.
- [x] Test order preservation, range expansion, duplicates and persistence.

## Phase 3 — Plex connectivity and matching (MVP)
- [x] Implement manual Plex server URL/token connection and validation.
- [x] Implement Plex account sign-in/authorisation using verified supported API flow.
- [x] Securely persist credentials and redact secrets.
- [x] Discover accessible Plex libraries and verify the connected account as the sole permitted playlist owner.
- [x] Fetch and paginate movies and individual episodes.
- [x] Match by stable provider IDs; add validated fallback and manual resolution.
- [x] Show per-destination available/missing/unmatched states.
- [x] Test authentication errors, unavailable servers and ambiguous matches.

## Phase 4 — Plex playlist sync (MVP)
- [x] Implement documented Plex mixed-media video playlist API and ordering; fixture-tested, live behaviour unverified.
- [x] Create a managed playlist for an authorised destination user.
- [x] Publish playable items in exact canonical order.
- [x] Retain missing entries in Ordarr without publishing broken placeholders.
- [x] Reconcile additions, removals and reorderings safely.
- [x] Detect newly indexed movies/episodes and insert at intended positions.
- [x] Add manual and scheduled sync (optional event triggers deferred).
- [x] Implement retry, idempotency, sync history and conflict policy.
- [x] Ensure unrelated playlists are never changed.
- [x] Test missing-then-available scenarios, repeated entries, API failures and restarts.
- [x] Perform and document optional live Plex verification; flag unverified behaviours if unavailable.

## Phase 5 — Portable formats and subscriptions (MVP)
- [x] Define and version YAML/JSON collection schema.
- [x] Implement safe file import and export with round-trip tests.
- [x] Implement direct URL and GitHub raw-file subscriptions.
- [x] Support per-collection automatic apply or approval-required updates.
- [x] Show source diffs and pending approvals.
- [x] Retain last valid source on fetch or validation failure.
- [x] Add URL/redirect restrictions, file size limits, timeouts and conditional fetches.
- [x] Test malformed inputs, upstream reordering and local edit/fork behaviour.

## Phase 6 — MVP release readiness
- [x] Run unit, integration-style and end-to-end UI tests.
- [x] Build and smoke-test production Docker image and Compose deployment.
- [x] Verify persistent data after container recreation and schema migration.
- [x] Validate administrator security, CSRF and secret handling.
- [x] Document installation, Plex setup, backup/restore and troubleshooting.
- [x] Measure and record Docker image size and idle CPU/RAM under stated conditions.
- [x] Audit `DESIGN.md` acceptance criteria and record known limitations.
- [x] Tag tested MVP/Phase 7 development candidate `v0.7.0-rc.1` (live integrations unverified).

## Phase 7 — Sonarr/Radarr (post-MVP)
- [x] Connect and validate optional Sonarr/Radarr instances.
- [x] Show monitored, queued, downloading and missing status.
- [x] Implement manual requests for confirmed matches.
- [x] Add opt-in automatic requests, disabled by default.
- [x] Add request limits, deduplication, exclusions, approval controls and audit history.
- [x] Test request effects and recovery using mock APIs and optional live instances.

## Phase 8 — Jellyfin (post-MVP)
- [x] Review the official user, library and playlist API contracts; document remaining publishing checks.
- [ ] Validate Jellyfin playlist API, mixed-media order and permissions.
- [x] Implement user-scoped connection, library discovery, complete scans, exact-ID matching and manual resolution.
- [ ] Implement ordered playlist publishing and reconciliation.
- [ ] Test newly available media insertion and missing media handling.
- [x] Document discovery/matching differences from Plex and live verification status; extend after playlist publishing.

## Phase 9 — Further enhancements
- [ ] Optional Emby/other media-server adapters.
- [ ] AI integration for generating collection files.
- [ ] Community collection catalogue and discovery.
- [ ] Per-user watch progress and next-unwatched entry.
- [ ] Advanced subscription overrides and conflict handling.
- [ ] Consider multi-user management after single-admin MVP.

## Additional integration and safety acceptance checks
- [x] Export running Compose deployment settings from Settings and verify env-file import without exporting credentials.
- [x] Publish only under the verified administrator Plex account (fixture-tested; live ownership unverified).
- [x] Keep local Ordarr administrator authentication separate from Plex account connection authorisation.
- [x] Support both Plex account sign-in and manual Plex server URL/token connection.
- [x] Restrict playlist writes to Ordarr-created or explicitly adopted playlists; test unrelated playlist protection.
- [x] Persist stable managed-playlist IDs and last successfully published sequences.
- [x] Validate proposed playlist changes and test recovery from interrupted/failed sync without clearing valid playlists.
- [x] Verify sync reconciliation after external playlist changes and retry without unintended duplicates.
- [x] Record which Plex integration scenarios passed mocked tests versus live-server tests.
- [x] Do not mark real-server compatibility verified until exercised against a real Plex server.
- [x] Post-MVP: enforce opt-in automatic Sonarr/Radarr requests with limits, deduplication, exclusions and audit history.


## Milestone notes

See the [validation wiki](https://github.com/trentnbauer/Ordarr/wiki/Validation),
[playlist sync](https://github.com/trentnbauer/Ordarr/wiki/Playlist-sync), and
[portable formats](https://github.com/trentnbauer/Ordarr/wiki/Portable-format).
Checked integration features are implemented with production adapters and tested
using disposable fixtures. **No live Plex server/account has been verified.**
Other-user publishing, webhook triggers and unrelated playlist adoption are unavailable.
README, environment examples and contribution guidance are present. Distribution
licensing remains the owner's decision; the repository is currently unlicensed.

Phase 7 now provides real v3 adapters, cached statuses, confirmed whole-media requests,
approvals, disabled-by-default automation with collection/server opt-ins, rolling
limits, exclusions, durable attempt history and uncertain-write recovery. Tests use
HTTP fixtures; live Sonarr/Radarr compatibility remains unverified. See the
[acquisition wiki](https://github.com/trentnbauer/Ordarr/wiki/Acquisition).

Phase 8 has read-only Jellyfin connectivity and matching. Indexed matches are not
labelled playable. Ordered playlist publishing, playback verification and playlist
recovery tests remain unfinished. See the
[Jellyfin wiki](https://github.com/trentnbauer/Ordarr/wiki/Jellyfin) for contracts and
explicit live-verification limits.
