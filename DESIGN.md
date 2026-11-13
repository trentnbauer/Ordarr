# Ordarr — Product Design Document

**Status:** Draft · **Version:** 0.1

## 1. Overview

Ordarr is a self-hosted web application for creating, importing, sharing and synchronising ordered watch lists containing both movies and individual television episodes. Plex is the initial target, with Jellyfin and other media servers planned. Ordarr owns the canonical order; destination playlists are generated views of that order.

**Core requirement:** A watch order can interleave episodes from different series with movies in any sequence. Entries remain in the canonical order even if their media is not yet available. When the destination media server detects newly acquired media, Ordarr inserts it into the correct position in the published playlist.

Ordarr does not stream, transcode or download media itself.

## 2. Collection model

### Collection types

- **Manual:** Authored and edited in Ordarr.
- **Imported:** Created from a local file or URL.
- **Subscribed:** Linked to a remote definition with update checks.
- **Forked:** Independently editable copy of another collection.

### Supported entries

- Movies and individual episodes, freely interleaved.
- Whole seasons and episode ranges, expanded to ordered entries.
- Repeated entries when intentionally specified.
- Missing, unreleased and unmatched items.

Each collection has an ID, title, description, optional cover art, tags, source and creator information, and timestamps. Each entry retains its position, media type, external identifiers and any user-defined notes. An occurrence-specific ID distinguishes intentional repetitions.

### Editor

Users can search for titles, add individual episodes or ranges, reorder by drag-and-drop or keyboard, bulk-select, remove, duplicate, preview and save changes. Editing a subscribed collection should offer a fork or explicit local overrides rather than silently changing the upstream definition.

## 3. Media identification

Use provider-independent identifiers in portable definitions, with TMDB and TVDB IDs where appropriate and IMDb as supplementary metadata. TV episodes must specify a series identifier plus season/episode or a stable episode identifier, including the numbering scheme when relevant.

Matching order:

1. Exact external identifier.
2. Validated cross-provider mapping.
3. Title, year and episode metadata comparison.
4. Manual confirmation for ambiguous matches.

Match results are stored per destination server. Never silently accept ambiguous fuzzy matches.

Availability states include **available**, **missing**, **monitored**, **downloading**, **unreleased**, **unmatched** and **unavailable**. A missing item on one server may be available on another.

## 4. Canonical order and synchronisation

Ordarr always retains the full intended sequence, regardless of media availability.

Example canonical sequence:

| Position | Entry | Available? |
| --- | --- | --- |
| 1 | Movie A | Yes |
| 2 | Series A S01E01 | Yes |
| 3 | Series B S01E03 | No |
| 4 | Movie B | Yes |
| 5 | Series A S01E02 | No |
| 6 | Movie C | Yes |

The initial published playlist contains entries 1, 2, 4 and 6. Once position 3 is available in the destination media server, it is inserted between 2 and 4. Position 5 is inserted between 4 and 6 when available. The canonical sequence is never renumbered merely because content is absent.

### Sync triggers

- Configurable periodic library checks.
- Supported media-server events/webhooks.
- Manual synchronisation.
- Optional Sonarr/Radarr monitoring as an additional signal.

A download is not considered playable until the destination media server exposes the corresponding item.

### Sync rules

- Publish available items in exact canonical order.
- Preserve deliberate repeated entries when the destination supports them.
- Keep unavailable entries in Ordarr while reconciling destination playlists.
- Reconcile source additions, removals and reordering.
- Avoid unintended duplicates and unnecessary playlist recreation.
- Retry transient failures and retain the last valid state.
- Never modify unrelated user playlists or original media files.
- Maintain separate status, playlist IDs and sync history for each destination and authorised user.
- Offer preview, manual sync, automatic sync, pause and full rebuild.

For externally modified Ordarr-managed playlists, offer **Ordarr-managed** (restore canonical order), **approval required** (review differences) and **paused** policies. Default to Ordarr-managed for playlists explicitly created by Ordarr.

## 5. Media-server integrations

### Plex — initial integration

Connect to Plex, discover libraries, match movies and individual episodes, and create/update ordered **video playlists** for authorised users. Mixed-media watch orders should use playlists rather than relying on native Plex collections. Optional native collections may be offered for browsing, subject to platform limitations.

### Jellyfin — planned

Support library discovery, matching, ordered playlists and updates using the available Jellyfin API. Validate mixed-media playback and playlist ownership against a real instance before declaring parity with Plex.

### Additional servers

Use a replaceable integration boundary so other servers, such as Emby, can be supported without changing portable collection files. Surface unsupported capabilities clearly.

## 6. Sonarr and Radarr

Integrations are optional. Display whether missing entries are monitored, queued or downloading; link to their entries in Sonarr/Radarr; optionally request missing movies or episodes after explicit user approval. Automatic requests are off by default. Media acquired through any method should still be detected through Plex/Jellyfin.

## 7. Import, export and subscriptions

Accept local **YAML/JSON**, direct URLs and GitHub-hosted definitions. Export the same portable format without local file paths or server-specific IDs. Validate schema versions, identifiers and episode ranges before importing. Treat external sources as untrusted.

Example schema (illustrative, not a verified watch order):

```yaml
version: 1
id: example-watch-order
name: Example Watch Order
items:
  - type: movie
    tmdb_id: 1771
  - type: episode
    tmdb_id: 83867
    season: 1
    episode: 1
  - type: episode_range
    tmdb_id: 83867
    season: 1
    start: 2
    end: 4
  - type: movie
    tmdb_id: 299537
```

The exact schema, episode numbering semantics and identifier conventions must be versioned and documented before public sharing.

Subscriptions periodically check for changes, show a diff of additions/removals/reordering, and support automatic updates or manual approval. Preserve the last valid version on failure. Provide source attribution and last-update timestamps. A community catalogue is a later optional enhancement, not a runtime dependency.

## 8. Web interface

**Dashboard:** Collection totals, missing media, recent acquisitions, sync history, integration health and items needing attention.

**Collections:** Grid/list views, search, filtering, sorting, creation, duplication and bulk sync.

**Collection detail:** Artwork, metadata, complete ordered sequence including missing entries, availability by server, destination playlists, edit/import/export actions, and sync controls.

**Editor:** Drag-and-drop and accessible keyboard reordering, multi-select, series/season expansion, episode-range insertion and preview.

**Discover:** Optional browsable community collection catalogue and subscription controls.

**Settings:** Media servers; Sonarr/Radarr; collection/sync defaults; subscriptions; metadata providers; system health, backup and logs.

Design for desktop and mobile. Long collections should remain responsive and searchable.

## 9. Optional watch progress

Show watched/unwatched entries, next unwatched entry and collection completion percentage. Read per-user playback history where supported, with manual overrides. Repeated entries need occurrence-specific progress handling. Playback progress never changes canonical order.

## 10. Technical and operational requirements

Implementation framework, database and internal architecture are left to the developer. Requirements:

- Docker and Docker Compose deployment with persistent application data.
- Environment-based configuration; no credentials committed to source control.
- Reverse-proxy compatibility and secure administration.
- Secure storage and redaction of service credentials and API tokens.
- Least-privilege integration permissions where supported.
- Safe validation of imported files and remote URLs, including protection against internal-network URL abuse.
- Health checks, logs, backup/restore and graceful recovery from unavailable integrations.
- Idempotent synchronisation and bounded API usage.
- Responsive handling of collections with thousands of entries.
- No changes to original media files.

## 11. Edge cases

- Missing media added later, removed media, renamed files and changed library locations.
- Multiple Plex/Jellyfin libraries, users and servers.
- Duplicate entries, multi-episode files, specials, alternate episode numbering and multiple editions.
- Unreleased titles and entries without reliable external identifiers.
- Temporary server outages, expired credentials and partial sync failures.
- Upstream subscription changes conflicting with local edits.
- Manual changes to destination playlists.
- Incomplete seasons, deleted source definitions and malformed imports.

## 12. Release phases

| Phase | Scope | Priority |
| --- | --- | --- |
| 1 | Foundation, collection model, editor and matching | MVP |
| 2 | Plex connection and ordered playlist publishing | MVP |
| 3 | Automatic detection and insertion of new media | MVP |
| 4 | Portable import/export and subscriptions | MVP |
| 5 | Sonarr/Radarr visibility and optional requests | Later |
| 6 | Jellyfin integration | Later |
| 7 | Community discovery and watch progress | Later |
| 8 | Additional media servers and enhancements | Later |

## 13. MVP acceptance criteria

- Create and edit a sequence mixing movies and individual TV episodes.
- Save and reload the sequence without losing ordering or missing entries.
- Match media in a connected Plex library.
- Publish an ordered Plex video playlist.
- Automatically insert newly available media at the intended position.
- Re-run synchronisation without creating unintended duplicates.
- Import/export validated portable YAML/JSON collections.
- Subscribe to a remote collection definition and reconcile updates.
- Handle failed imports, unavailable servers and partial sync failures without destroying valid collection data.
- Deploy and persist data using Docker Compose.

## 14. Open decisions

- Local overrides versus mandatory forks for subscribed collections.
- Default subscription-update approval policy.
- Multi-user playlist ownership and authentication approach.
- Episode numbering schemes and multi-episode matching.
- Handling duplicate entries on destinations with limitations.
- Timing and scope of watch-progress support.
- Whether optional media requests should be individual, batch or collection-wide.

**Design principle:** The canonical collection is the source of truth. Missing media remains in that collection, ordering is intentional, and Plex/Jellyfin playlists are destination-specific projections.
