# Ordarr — Development Checklist

Track progress by changing `- [ ]` to `- [x]`. Tasks are grouped by implementation phase. Complete the **MVP** before later enhancements unless dependencies suggest otherwise.

## Phase 1 — Foundation (MVP)

- [ ] Initialise application repository and development environment.
- [ ] Create Docker Compose deployment with persistent data and health checks.
- [ ] Implement application configuration and secure credential handling.
- [ ] Implement admin authentication and settings.
- [ ] Define versioned collection and entry data models.
- [ ] Store ordered movie and episode entries, including missing and repeated items.
- [ ] Implement collection create, edit, duplicate and delete.
- [ ] Build collection library, dashboard and detail views.
- [ ] Build editor with search, episode ranges and accessible reordering.
- [ ] Integrate metadata lookup and external identifiers.
- [ ] Implement exact matching, cross-reference and manual match resolution.
- [ ] Add tests for ordering, repeated entries and missing entries.

## Phase 2 — Plex integration (MVP)

- [ ] Add Plex connection configuration and connection testing.
- [ ] Discover Plex libraries and authorised users.
- [ ] Match movies and episodes to Plex library items.
- [ ] Publish mixed-media ordered video playlists.
- [ ] Save playlist IDs and destination-specific sync state.
- [ ] Update existing Ordarr-managed playlists without touching unrelated playlists.
- [ ] Handle duplicate entries and playlist ownership limitations.
- [ ] Add manual sync, sync preview, logs and error messages.
- [ ] Verify actual Plex playback order with interleaved films and episodes.

## Phase 3 — Automatic media updates (MVP)

- [ ] Store canonical sequence independently of playlist availability.
- [ ] Track available, missing and unmatched entries per server.
- [ ] Implement periodic library scanning.
- [ ] Support Plex notifications/events where practical.
- [ ] Detect newly available movies and episodes.
- [ ] Insert newly available items into their exact canonical positions.
- [ ] Reconcile media removals and source reordering.
- [ ] Make sync idempotent and safe to retry.
- [ ] Add automatic/manual sync settings and conflict policies.
- [ ] Test outages, partial failures, repeats and delayed acquisitions.

## Phase 4 — Import, export and subscriptions (MVP)

- [ ] Finalise and document versioned YAML/JSON schema.
- [ ] Implement file import and export.
- [ ] Implement direct URL and GitHub-hosted imports.
- [ ] Validate identifiers, episode ranges and untrusted source URLs.
- [ ] Add subscribed collection sources and scheduled update checks.
- [ ] Show upstream changes before applying them.
- [ ] Support automatic or approval-based updates.
- [ ] Implement collection forks or explicit local overrides.
- [ ] Preserve previous valid data on import/update failure.
- [ ] Test collection portability across different Plex libraries.

## Phase 5 — Sonarr and Radarr (Later)

- [ ] Add Sonarr connection and monitoring status.
- [ ] Add Radarr connection and monitoring status.
- [ ] Link missing episodes/movies to their *arr entries.
- [ ] Display queued, monitored and downloading states.
- [ ] Support optional user-approved missing-media requests.
- [ ] Keep automatic acquisition requests disabled by default.
- [ ] Confirm media becomes playable only after Plex/Jellyfin library detection.

## Phase 6 — Jellyfin (Later)

- [ ] Add Jellyfin connection and library discovery.
- [ ] Implement Jellyfin movie and episode matching.
- [ ] Validate ordered mixed-media playlist support against a real server.
- [ ] Implement playlist creation, updates and ownership handling.
- [ ] Add destination-specific availability and sync status.
- [ ] Test cross-server collection portability.

## Phase 7 — Discovery and watch progress (Later)

- [ ] Add optional community collection catalogue.
- [ ] Support browsing, searching and subscribing to community watch orders.
- [ ] Read watched/unwatched status from supported servers.
- [ ] Display next unwatched entry and completion progress.
- [ ] Support per-user progress and manual overrides.
- [ ] Handle repeated entries in progress tracking.

## Phase 8 — Polish and release (Later)

- [ ] Test mobile and desktop layouts and keyboard accessibility.
- [ ] Optimise very large collections and API usage.
- [ ] Add backup/restore and operational health information.
- [ ] Document installation, configuration and troubleshooting.
- [ ] Add integration tests for malformed imports and failed syncs.
- [ ] Review credential protection, logs and remote URL safety.
- [ ] Prepare release notes and versioned Docker image.

## MVP completion gate

- [ ] Create a collection with interleaved movies and episodes.
- [ ] Import and export the collection in a portable format.
- [ ] Publish it as an ordered Plex playlist.
- [ ] Leave missing entries in Ordarr without breaking playback order.
- [ ] Download or add a previously missing item and confirm automatic insertion at the correct position.
- [ ] Confirm repeated synchronisation creates no unintended duplicates.
- [ ] Confirm a failed sync leaves valid collection data intact.
- [ ] Deploy and restart Ordarr via Docker Compose without data loss.
