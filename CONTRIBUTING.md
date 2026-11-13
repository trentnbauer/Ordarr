# Contributing

Read AGENTS.md, DESIGN.md and TODO.md before changing the application. Implement MVP
phases in order and keep each milestone runnable. Preserve deliberate repeats and
missing entries when implementing collection ordering. Production adapters must use
real service APIs; keep fixture-backed and live-server verification distinct.

Use Go 1.25+, a C compiler and Make. Run `make check test build` and the native smoke
test before submitting changes. Docker/deployment changes also require building the
image and running `python3 scripts/smoke.py --compose`. Add focused tests for behavior,
update documentation, and check off only work that was implemented and verified.

SQL migrations live in `internal/store/migrations` with sequential, zero-padded
filenames. Add migrations rather than editing already released ones. Keep SQL
transactional and test upgrade, failure and persistence behavior.

Never commit credentials, local `.env`, database files or generated binaries. Do not
log tokens, passwords, request bodies or session cookies. The repository owner has
not selected a distribution licence; do not assume an open-source licence grant.
