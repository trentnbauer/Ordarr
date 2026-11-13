#!/usr/bin/env bash
# Checks next-version.sh. Run from anywhere: .github/scripts/next-version.test.sh
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected labels tags
  local got
  got="$(printf '%s\n' "$4" | "$here/next-version.sh" "$3" 2>/dev/null)"
  if [ "$got" = "$2" ]; then echo "ok   $1"; else echo "FAIL $1: expected '$2', got '$got'"; fail=1; fi
}
refuses() { # name labels tags
  if printf '%s\n' "$3" | "$here/next-version.sh" "$2" >/dev/null 2>&1; then echo "FAIL $1: should have been refused"; fail=1; else echo "ok   $1"; fi
}

tags=$'v1.2.3\nv1.2.10\nv1.10.0\nv1.9.9\nv2.0.0-rc.1\nnot-a-version'
check "fix"                    "1.10.1" "release: fix"   "$tags"
check "minor resets the patch" "1.11.0" "release: minor" "$tags"
check "major resets the rest"  "2.0.0"  "release: major" "$tags"
check "sorts numbers, not text" "1.10.1" "release: fix"  $'v1.9.9\nv1.10.0'
check "pre-releases are ignored" "1.0.1" "release: fix"  $'v1.0.0\nv9.9.9-rc.1'
check "no tags: first fix"     "0.0.1"  "release: fix"   ""
check "no tags: first minor"   "0.1.0"  "release: minor" ""
check "no tags: first major"   "1.0.0"  "release: major" ""
check "other labels are ignored" "1.3.0" "bug, release: minor, docs" "v1.2.9"
check "none releases nothing"  ""       "release: none"  "$tags"
refuses "no release label"     "bug,docs" "$tags"
refuses "empty labels"         "" "$tags"
refuses "two release labels"   "release: fix,release: major" "$tags"
refuses "none plus a bump"     "release: none,release: fix" "$tags"

exit $fail
