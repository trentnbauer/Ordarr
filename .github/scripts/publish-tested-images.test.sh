#!/usr/bin/env bash
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/publish-tested-images.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/digests"
cat > "$tmp/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2 $3" in
  'buildx imagetools create')
    printf '%s\n' "$@" > "$CALLS/create"
    exit "${FAIL_CREATE:-0}"
    ;;
  'buildx imagetools inspect')
    printf '%s\n' "$@" > "$CALLS/inspect"
    printf '%s\n' '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}'
    ;;
  *) echo 'Unexpected Docker command (publishing must not rebuild)' >&2; exit 99 ;;
esac
MOCK
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" IMAGE=ghcr.io/example/ordarr DIGEST_DIR="$tmp/digests" CALLS="$tmp/calls"
export IMAGE_TAGS=$'ghcr.io/example/ordarr:1.2.3\nghcr.io/example/ordarr:latest'
mkdir "$CALLS"
printf -v a '%064d' 0
printf -v b '%064d' 1
printf -v c '%064d' 2
reset() { rm -f "$DIGEST_DIR"/* "$CALLS"/*; }
reject() {
  if bash "$script" > "$tmp/stdout" 2> "$tmp/stderr"; then echo 'Invalid publication unexpectedly succeeded' >&2; exit 1; fi
  test ! -e "$CALLS/create"
}
# Exactly the provided tags and content-addressed images are promoted; no build command is accepted.
touch "$DIGEST_DIR/$a" "$DIGEST_DIR/$b"
bash "$script" > "$tmp/stdout"
printf '%s\n' buildx imagetools create --tag ghcr.io/example/ordarr:1.2.3 --tag ghcr.io/example/ordarr:latest "${IMAGE}@sha256:$a" "${IMAGE}@sha256:$b" > "$tmp/expected"
cmp "$tmp/expected" "$CALLS/create"
test -e "$CALLS/inspect"
# No untested, incomplete or malformed digest sets may publish tags.
reset; reject
reset; touch "$DIGEST_DIR/$a"; reject
reset; touch "$DIGEST_DIR/$a" "$DIGEST_DIR/$b" "$DIGEST_DIR/$c"; reject
reset; touch "$DIGEST_DIR/bad" "$DIGEST_DIR/$b"; reject
reset; touch "$DIGEST_DIR/$a" "$DIGEST_DIR/$b"; export IMAGE_TAGS=''; reject
# A prerelease does not acquire a latest tag from the promotion step.
export IMAGE_TAGS=ghcr.io/example/ordarr:1.2.3-rc.1
bash "$script" > "$tmp/stdout"
if grep -q latest "$CALLS/create"; then echo 'Prerelease unexpectedly tagged latest' >&2; exit 1; fi
# Registry errors stop the job, with no later inspect operation reported as success.
rm -f "$CALLS"/*
export FAIL_CREATE=17
if bash "$script" > "$tmp/stdout" 2> "$tmp/stderr"; then echo 'Registry failure unexpectedly succeeded' >&2; exit 1; fi
test -e "$CALLS/create"
test ! -e "$CALLS/inspect"
echo 'Image promotion tests passed.'
