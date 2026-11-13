# Codex Instructions — Ordarr

Build a **working, deployable application**, not a plan, UI-only prototype or mock service. `DESIGN.md` defines requirements; `TODO.md` is the implementation checklist.

## Decisions
- **Mandatory:** self-hosted Docker deployment, persistent storage and low idle resource usage.
- **Preferred stack:** Go, embedded server-rendered HTML/HTMX and small JavaScript, SQLite; single container without Redis or a separate database. Deviate only with a documented, material benefit.
- **MVP:** single local administrator; Plex manual URL/token **and** Plex account sign-in; canonical mixed movie/episode watch orders; safe Plex video-playlist sync; missing-media insertion; YAML/JSON import/export and subscriptions with per-collection update approval policy.
- **Later:** Sonarr/Radarr requests (including optional automatic requesting, disabled by default), Jellyfin, community discovery and progress tracking.

## Working rules
1. Inspect the repository and these documents before changing code.
2. Implement the Plex MVP phases in `TODO.md` order. Do not start post-MVP features until MVP acceptance criteria are met.
3. Maintain one canonical ordered collection independent of Plex; retain missing/unmatched items and deliberate repeats.
4. Never guess ambiguous media matches or silently reorder entries.
5. Do not change unrelated Plex playlists or delete/modify source media.
6. Use real service API adapters in production; mocks are only for automated tests and development fixtures.
7. Verify API assumptions against authoritative documentation and, where possible, a disposable live test server. Mark untested external behaviour clearly.
8. Implement migrations, error handling, bounded retries, secure credential storage, structured logs and automated tests.
9. Build and test the Docker image and Compose deployment. Document measured resource usage rather than inventing targets.
10. After each coherent feature: run relevant tests/builds, fix failures, update documentation and mark verified `TODO.md` items `[x]`.
11. Never mark partial, mocked-only production behaviour or untested features complete. Distinguish passing mocked integration tests from real-server validation.
12. Keep the app operable after each phase; avoid placeholder routes or nonfunctional buttons presented as complete.

## Ambiguity and safety
Choose a conservative, maintainable default and record it when a decision is reversible. Ask only when a decision materially changes the product, requires credentials, or risks destructive changes. Do not block the rest of development waiting for access to a live Plex server; build a tested adapter and explicitly record the remaining live-verification steps.

## Completion criteria
The MVP is complete only when its features work together end-to-end, automated tests and production builds pass, Docker Compose starts cleanly with persistent data, and all outstanding live-integration limitations are explicitly documented. Summarise completed work, remaining TODOs, tests run and any unverified external integrations at the end of each implementation session.

## Additional mandatory implementation guardrails
- Use the connected administrator's authorised Plex account as the MVP playlist owner; do not assume permission to publish into other users' accounts.
- Keep local Ordarr administrator authentication separate from Plex sign-in, which authorises a Plex connection. Support both Plex sign-in and manual server URL/token setup.
- Restrict writes to playlists created by Ordarr or explicitly adopted by the administrator. Never touch unrelated playlists.
- Persist a stable destination playlist ID and last successfully published ordered sequence for each managed playlist. Reconcile actual destination state before writing.
- Validate changes before publishing; handle interrupted and failed writes without intentionally emptying or truncating previously valid playlists. Test retry and recovery paths.
- Keep automatic Sonarr/Radarr acquisition disabled by default. When implementing it post-MVP, add configurable limits, deduplication, exclusions and audit logging.
- Successful compilation or mocked tests are not proof of live service compatibility. Use real production API adapters, validate against documented APIs, and clearly distinguish mocked test results from live-server verification.
- Prioritise data integrity, low idle CPU/RAM usage and reliable synchronisation over visual polish or additional features. Never mark simulated, incomplete or untested behaviour complete.
