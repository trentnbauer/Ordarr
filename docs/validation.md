# Development validation

## Foundation milestone (historical)

Validated in the managed Linux amd64 development environment on 13 November 2026.
Native compiler: Go 1.27.4, GCC 14.2.0. Docker builder: Go 1.25 on Debian Bookworm.
Docker daemon: 29.9.2; Compose: 2.40.3.

### Foundation checks

- `make check test build`: formatting, `go vet`, race-enabled tests and native build
  passed. Ten top-level Go tests ran across the application and SQLite packages;
  the CSRF test additionally runs five invalid-request subtests.
- SQLite checks exercise first migration, repeat startup, durable administrator and
  sessions, singleton enforcement under concurrency, expiry/revocation, bounded
  retention, rollback after a failed migration and rejection of newer schemas.
- HTTP checks exercise setup/login/logout, bad credentials, input validation,
  request-size limits, CSRF, wrong-origin rejection, HTML escaping, secure cookie
  flags, rate-limit expiry and an unavailable database health response.
- `python3 scripts/smoke.py`: passed against the compiled binary. It restarts the
  process with the same SQLite directory and browser cookies, confirms persisted
  authentication, checks revocation and signs in again. Both shutdowns exited zero
  and logged successful graceful shutdown.
- `CHROMIUM_PATH=/usr/bin/chromium node scripts/browser-smoke.cjs`: passed real
  browser form submission, navigation, failed/successful login and a 390-pixel
  mobile layout without horizontal overflow; desktop and mobile screenshots were
  inspected. Browser testing caught and fixed a referrer-policy issue that made
  same-origin form POSTs send a null Origin in Chromium.

- Docker image build passed with CGO and a non-root Debian runtime. The final image
  `sha256:c001255a3c9bb7cd7210cc7b4c83c0142ee8ca0d238180000d7eb6bbe8ff123e`
  passed `python3 scripts/smoke.py --compose`: container replacement retained both
  administrator and session data; logout invalidated the old token; both container
  shutdowns exited zero and logged graceful shutdown. The isolated test volume was
  removed afterwards.
- The normal Compose project is running healthy, bound to loopback port 8080 with
  its own named volume and no administrator pre-created. Functional health and
  first-run setup responses returned HTTP 200. Environment configuration was not
  modified or published.

### Foundation resource sample

With the foundation idle on its first-run screen, no administrator/media configured,
5 available CPU cores and 32 GiB reported memory, run:

```sh
python3 scripts/measure.py
```

The final local image occupied **86,913,914 bytes (82.89 MiB)** according to
`docker image inspect` (local image size, not a compressed registry download).
The application PID 1 reported **12,216 KiB RSS (11.93 MiB)** in `/proc/1/status`.
Three consecutive five-second process CPU samples were **0.000%, 0.000%, 0.000%**
of one CPU core at the kernel tick resolution; this does not establish zero CPU
usage over longer workloads.

Nested Docker reported about 3.22 GiB in `docker stats`, while the process cgroup
path was `0::/` and its visible `memory.current` included the outer environment.
These cgroup counters are not isolated application measurements in this runtime,
so the figures above use process RSS and process CPU ticks instead. Re-measure
container-level usage on a host with dedicated container cgroups before setting
production resource limits. Samples exclude compilation and startup hashing.

## Collection editor milestone

Validated on 13 November 2026 in the same managed Linux amd64 environment:

- `make check test build` passed: formatting, `go vet`, **18 top-level tests** with
  race detection, and the native build. New cases cover deterministic episode/season
  expansion, specials, repeated identifiers with separate entry IDs, editing, bulk
  movement/removal, full-sequence validation, copy independence, filtering/pagination
  and the 1,000-entry bound. Existing authentication tests remain passing.
- A forced SQLite trigger failure during reorder verified rollback of all positions
  and the revision. Two simultaneous edits with the same revision produced exactly
  one success and one conflict. A retained entry referenced from another table
  survived reorder without being deleted/recreated.
- A schema-1 database containing an administrator and session upgraded to schema 2
  without losing either. Collections and their stable IDs survived closing/reopening
  the database.
- Native and Compose smoke tests passed with a mixed movie/specials sequence and a
  deliberate repeat. The same five IDs in the same order survived process/container
  replacement. Logout revocation, sign-in and graceful shutdown also passed.
- The Chromium browser test passed creation, movie editing, inclusive ranges, repeats,
  keyboard movement with focus restoration, real native drag ordering and reload,
  cancelled-drag recovery, bulk move/removal, independent fork/deletion, search/filter
  and mobile overflow checks. Whole-season addition and movie editing also passed
  in a context with JavaScript disabled. Mobile and desktop screenshots were inspected.
- Browser validation exposed a changing live-status height that shifted drag targets.
  The status area now reserves space, and dragover marks a destination without
  changing the DOM order until drop. The final saved order is checked after reload.
- Docker image `ordarr:0.2.0-dev`, ID
  `sha256:4d81cd7231bd5915566a72e8ea6109d1c212d47c88e082206621a9bb67d98d58`,
  built successfully and passed the complete isolated Compose smoke test. Its test
  project and volume were removed afterwards.
- Before upgrading the normal running instance, a consistent stopped-service backup
  was saved outside the checkout at `/workspace/ordarr-backups/pre-collections-MotxUE/data`.
  The upgraded database passed SQLite integrity checks at schema 2. Administrator
  and session rows exactly matched the pre-upgrade snapshot. Its checked post-migration
  copy is under the same backup directory at `after-migration`. The existing named
  volume was preserved. The updated instance is running healthy.
- The idle schema-2 instance measured **87,000,026 bytes (82.97 MiB)** for the local
  image and **11,676 KiB (11.40 MiB)** process RSS. Three five-second process CPU
  samples were 0.000% of one core at kernel tick resolution. These measurements
  used `python3 scripts/measure.py` on the otherwise idle normal instance; the
  disposable fixture collections had been removed with their test projects.
  The nested cgroup limitation noted above still applies. These are not load results.
- No environment settings were modified or published. GitHub Actions is configured
  for Go checks, Docker build and Compose smoke, but has not run on GitHub.

## Manual Plex connectivity milestone

Validated on 13 November 2026 in the managed Linux amd64 environment:

- `make check test build` passed: formatting, `go vet`, **31 top-level tests** with
  race detection and a native build. All prior authentication/collection tests remain
  passing. Plex adapter tests use the real read-only adapter against disposable HTTP
  fixtures; none exercises a live Plex Media Server.
- Adapter cases cover documented `/library/sections/all` and 404-only legacy fallback,
  header/body pagination, partial/inconsistent responses, duplicate keys, credential
  headers, 401/403/498, rate limits, bounded retry/timeout behavior, refused redirects,
  untrusted TLS certificates, response-size limits and reflected tokens. Direct URL
  tests cover private/loopback support, plaintext consent and blocked metadata URLs.
- Vault tests verify AES-GCM round trips after reopen, fresh nonces, ciphertext
  tampering, wrong connection binding, concurrent atomic key creation, private file
  permissions, and failure for missing/corrupt/exposed keys. The application test
  confirms a missing key is not regenerated when stored credentials exist.
- Store/HTTP cases verify atomic snapshot rollback on an injected duplicate library
  key, retained last-good libraries after failed recheck, machine-ID pinning, token
  retention only at the same URL, optimistic conflicts, connection count bounds,
  authentication/CSRF and local-only deletion with cascading snapshot cleanup.
- `CHROMIUM_PATH=/usr/bin/chromium node scripts/plex-browser-smoke.cjs` passed
  manual connection, library discovery, encrypted token/key survival across an
  application restart, failed recheck with cached data, recovery, token rotation,
  response/log redaction, mobile overflow checks and local deletion. The fixture
  asserts all Plex requests are GETs. Mobile screenshots were inspected.
- The existing collection browser suite and native persistence smoke also passed.
  The native and Compose smoke now verify the private 32-byte credential key and
  its unchanged contents after process/container replacement. They continue to
  check mixed sequence/repeat persistence, logout revocation and graceful shutdown.
  Docker credential-bearing restart is not separately live-verified: token restart
  uses the native browser fixture; the container smoke verifies key persistence.
- `ordarr:0.3.0-dev`, image ID
  `sha256:ca4053acd5482b2fe6ed4b2952acd6a81481fb505c28eff278235b06d9a91681`,
  built successfully and passed the isolated Compose smoke. Its test volume/project
  was removed. No production fixture or simulated Plex adapter is installed.
- Before upgrading the normal instance, its stopped schema-2 data was backed up at
  `/workspace/ordarr-backups/pre-plex-1yzRRB/data`. The checked schema-3 copy is in
  `after-migration` under that directory. SQLite integrity passed and all administrator,
  session, collection and entry rows exactly matched the pre-upgrade snapshot.
  Private key creation was verified. The existing named volume was retained and
  the upgraded instance is running healthy. No live Plex credentials were configured.
- Idle schema-3 measurements were **87,127,386 bytes (83.09 MiB)** for the local
  image and **12,336 KiB (12.05 MiB)** process RSS. Three five-second process CPU
  samples were 0.000% of one core at kernel tick resolution. Measurements used
  `python3 scripts/measure.py`, without a configured live Plex connection or media
  scans. The previously documented nested-cgroup limitation still applies.
- Environment configuration and publication were unchanged. GitHub Actions configuration
  includes the new Go suites via `go test ./...`; no new GitHub run was inspected.

## Plex account authorisation milestone

Validated on 14 November 2026 in the managed Linux amd64 environment:

- The completed manual-connection milestone was committed and pushed to `main` as
  `bfe9767` before account work began.
- `make check test build` and the final `make check test` passed. The final standard
  suite contains **42 passing top-level tests and one optional browser case skipped**.
  The optional case was then explicitly enabled and passed separately, for all 43
  top-level cases exercised across those runs. Race detection was enabled throughout.
- Account adapter tests inspect the public Ed25519 JWK and verify real device JWT
  signatures, `kid`, audience, issuer, expiry, nonce, stable client identity and token
  scoping. They exercise pending/complete PIN flows, verified user/resource reads,
  nonce refresh, expired/malformed/anonymous identities, fixed API origins, rejected
  redirects, error redaction and no automatic retry of account operations.
- HTTP/store tests cover session-bound attempts, two-second polling throttles,
  expiry, pending limits, consumed-attempt replay, cross-session cancellation,
  logout cleanup, authentication/CSRF, failed refresh preservation and account
  revision conflicts. A failed account/resource verification marks linked-server
  library snapshots stale without sending a PMS request or changing the saved token. A different Plex user cannot silently replace the connected
  user. Reauthorisation rotates the device identifier and marks linked servers for
  recheck atomically. Explicit disconnect retains manual servers and collections.
- Account credentials were saved, the SQLite database was closed and reopened, and
  a new application loaded the same encrypted device private key, account token and
  server resource cache. The restored device completed fixture-backed refresh.
  An account without manual server connections still prevented missing-key replacement.
- `ORDARR_ACCOUNT_BROWSER=1 CHROMIUM_PATH=/usr/bin/chromium go test -race -count=1
  -run '^TestBrowserPlexAccountFlow$' -v ./internal/app` passed against a real app
  instance with test-only account/PMS transports. It covers sign-in start/cancel,
  the fixed external auth URL (not opened), account verification, explicit server
  selection, server-scoped connection, refresh, same-user reauthorisation, recheck,
  redaction, mobile layout and explicit local disconnect. The fixture checks that
  a PMS never receives the account token and that PMS requests are read-only.
- The existing collection browser suite, manual Plex browser suite, native smoke,
  Docker build and isolated Compose replacement smoke all passed. The credential
  key and canonical mixed order/repeats survived replacement. No real Plex account
  was authorised and no live Plex server was contacted by these checks.
- Image `ordarr:0.4.0-dev`, ID
  `sha256:e8f8023e3e8ea23ebaaf4e9d6fd495cb409a68bbaba3026fc788edba840da328`,
  built successfully and passed Compose smoke. Its disposable project/volume was
  removed. The normal instance was upgraded after a stopped schema-3 backup at
  `/workspace/ordarr-backups/pre-account-qAfONT/data`. The checked schema-4 copy is
  in `after-migration` alongside it. SQLite integrity, all pre-existing table columns
  and rows, server library snapshots and the encryption key were unchanged.
  The original named volume was retained and the account-capable instance is healthy.
- Idle schema-4 measurements: **87,230,010 bytes (83.19 MiB)**
  for the local image and **12,796 KiB (12.50 MiB)** process RSS.
  Three five-second process CPU samples were 0.000%, 0.000%, 0.000%
  of one core at kernel tick resolution. The instance had no active account/network
  scan. The nested-cgroup limitations described earlier still apply; this is not a
  production load result.
- Environment settings/publication were not changed. No new GitHub Actions result
  was inspected; local commands corresponding to its checks passed.

## Limits

Manual Plex connections, library discovery, the documented JWT/PIN/JWK account flow,
nonce refresh, verified connected-account identity and explicit server-resource
connections are implemented. Other-user enumeration/permission checks, paginated
movie/episode scans, matching, ambiguity resolution, availability and playlist sync
remain unimplemented. Provider metadata search, import/export and subscriptions also
remain open. The combined libraries/users and authentication/ambiguity checklist items
remain unchecked. No live Plex, Jellyfin or *arr instance has been verified; see
`plex-api.md` for API assumptions and remaining real-service checks. Unmatched entries
have no inferred availability or release status. No MVP release is tagged or registry
image published. This is not a full security audit or production load test. Password
change/recovery, external key management/rotation and distribution licensing remain open.
