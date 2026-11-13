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
- [ ] Implement manual Plex server URL/token connection and validation.
- [ ] Implement Plex account sign-in/authorisation using verified supported API flow.
- [ ] Securely persist credentials and redact secrets.
- [ ] Discover accessible Plex libraries and permitted users.
- [ ] Fetch and paginate movies and individual episodes.
- [ ] Match by stable provider IDs; add validated fallback and manual resolution.
- [ ] Show per-destination available/missing/unmatched states.
- [ ] Test authentication errors, unavailable servers and ambiguous matches.

## Phase 4 — Plex playlist sync (MVP)
- [ ] Verify Plex mixed-media video playlist API and ordering behaviour.
- [ ] Create a managed playlist for an authorised destination user.
- [ ] Publish playable items in exact canonical order.
- [ ] Retain missing entries in Ordarr without publishing broken placeholders.
- [ ] Reconcile additions, removals and reorderings safely.
- [ ] Detect newly indexed movies/episodes and insert at intended positions.
- [ ] Add manual sync, scheduled sync and optional event-triggered sync.
- [ ] Implement retry, idempotency, sync history and conflict policy.
- [ ] Ensure unrelated playlists are never changed.
- [ ] Test missing-then-available scenarios, repeated entries, API failures and restarts.
- [ ] Perform and document optional live Plex verification; flag unverified behaviours if unavailable.

## Phase 5 — Portable formats and subscriptions (MVP)
- [ ] Define and version YAML/JSON collection schema.
- [ ] Implement safe file import and export with round-trip tests.
- [ ] Implement direct URL and GitHub raw-file subscriptions.
- [ ] Support per-collection automatic apply or approval-required updates.
- [ ] Show source diffs and pending approvals.
- [ ] Retain last valid source on fetch or validation failure.
- [ ] Add URL/redirect restrictions, file size limits, timeouts and conditional fetches.
- [ ] Test malformed inputs, upstream reordering and local edit/fork behaviour.

## Phase 6 — MVP release readiness
- [ ] Run unit, integration-style and end-to-end UI tests.
- [ ] Build and smoke-test production Docker image and Compose deployment.
- [ ] Verify persistent data after container recreation and schema migration.
- [ ] Validate administrator security, CSRF and secret handling.
- [ ] Document installation, Plex setup, backup/restore and troubleshooting.
- [ ] Measure and record Docker image size and idle CPU/RAM under stated conditions.
- [ ] Audit `DESIGN.md` acceptance criteria and record known limitations.
- [ ] Tag MVP release only after required tests pass.

## Phase 7 — Sonarr/Radarr (post-MVP)
- [ ] Connect and validate optional Sonarr/Radarr instances.
- [ ] Show monitored, queued, downloading and missing status.
- [ ] Implement manual requests for confirmed matches.
- [ ] Add opt-in automatic requests, disabled by default.
- [ ] Add request limits, deduplication, exclusions, approval controls and audit history.
- [ ] Test request effects and recovery using mock APIs and optional live instances.

## Phase 8 — Jellyfin (post-MVP)
- [ ] Validate Jellyfin playlist API, mixed-media order and permissions.
- [ ] Implement connection, library discovery and matching.
- [ ] Implement ordered playlist publishing and reconciliation.
- [ ] Test newly available media insertion and missing media handling.
- [ ] Document differences from Plex and live verification status.

## Phase 9 — Further enhancements
- [ ] Optional Emby/other media-server adapters.
- [ ] Community collection catalogue and discovery.
- [ ] Per-user watch progress and next-unwatched entry.
- [ ] Advanced subscription overrides and conflict handling.
- [ ] Consider multi-user management after single-admin MVP.

## Additional integration and safety acceptance checks
- [ ] Publish MVP Plex playlists under the authorised administrator's Plex account and verify ownership/permissions.
- [ ] Keep local Ordarr administrator authentication separate from Plex account connection authorisation.
- [ ] Support both Plex account sign-in and manual Plex server URL/token connection.
- [ ] Restrict playlist writes to Ordarr-created or explicitly adopted playlists; test unrelated playlist protection.
- [ ] Persist stable managed-playlist IDs and last successfully published sequences.
- [ ] Validate proposed playlist changes and test recovery from interrupted/failed sync without clearing valid playlists.
- [ ] Verify sync reconciliation after external playlist changes and retry without unintended duplicates.
- [ ] Record which Plex integration scenarios passed mocked tests versus live-server tests.
- [ ] Do not mark real-server compatibility verified until exercised against a real Plex server.
- [ ] Post-MVP: enforce opt-in automatic Sonarr/Radarr requests with limits, deduplication, exclusions and audit history.

## Foundation milestone notes

See `docs/validation.md` for executed checks and `docs/decisions.md` for assumptions.
README, example environment configuration and contribution guidance are in place;
the Phase 0 documentation item remains open pending the owner’s distribution licence choice.
Settings currently displays account and instance configuration; password change/recovery
and media connection settings are not implemented. Phase 2 now provides the canonical
collection editor; Phase 3 (Plex connectivity and matching) is next. Whole seasons use
explicit episode counts; provider metadata lookup and availability checks remain unimplemented.
