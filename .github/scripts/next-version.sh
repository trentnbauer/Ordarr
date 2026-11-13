#!/usr/bin/env bash
# Works out the next release version from a merged pull request's labels.
#
#   next-version.sh "<comma-separated labels>" < list-of-existing-tags
#
# Labels (exactly one of these must be present):
#   release: fix    1.2.3 -> 1.2.4
#   release: minor  1.2.3 -> 1.3.0
#   release: major  1.2.3 -> 2.0.0
#   release: none   no release (documentation, CI and other changes that need no new image)
#
# Prints the new version without a leading "v", or nothing for "release: none".
# Exits 2 with a message on stderr if there is not exactly one release label.
# Only plain vMAJOR.MINOR.PATCH tags count: pre-release tags such as v1.2.3-rc.1 are ignored.
# With no tag yet the base is 0.0.0, so the first fix is 0.0.1, the first minor 0.1.0 and the first major 1.0.0.
set -euo pipefail

labels="${1:-}"
bump=""
count=0
IFS=',' read -ra all <<< "$labels"
for l in "${all[@]}"; do
  l="$(echo "$l" | sed 's/^ *//; s/ *$//')"
  case "$l" in
    "release: fix") bump=fix; count=$((count + 1)) ;;
    "release: minor") bump=minor; count=$((count + 1)) ;;
    "release: major") bump=major; count=$((count + 1)) ;;
    "release: none") bump=none; count=$((count + 1)) ;;
  esac
done
if [ "$count" -ne 1 ]; then
  echo "Expected exactly one of: release: fix, release: minor, release: major, release: none (found $count)." >&2
  exit 2
fi
[ "$bump" = none ] && exit 0

latest="$(grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sed 's/^v//' | sort -V | tail -n 1 || true)"
latest="${latest:-0.0.0}"
IFS='.' read -r major minor patch <<< "$latest"
case "$bump" in
  fix) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  major) major=$((major + 1)); minor=0; patch=0 ;;
esac
echo "$major.$minor.$patch"
