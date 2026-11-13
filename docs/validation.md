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

## Limits

Collections currently use explicit provider identities supplied by the administrator.
Provider metadata search, Plex connection/authentication, matching and playlist sync,
import/export and subscriptions remain unimplemented. No Plex, Jellyfin or *arr API
has been called or verified. Unmatched entries have no inferred availability or release
status. No release is tagged or registry image published. This is not a full security
audit or production load test. Password change/recovery and distribution licensing
remain open. The foundation measurements above are historical; measure again under
real library, matching and sync workloads before drawing MVP resource conclusions.
