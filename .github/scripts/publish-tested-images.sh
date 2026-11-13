#!/usr/bin/env bash
# Promote only the two image digests already built and health-tested in this run.
set -euo pipefail
shopt -s nullglob
digests=("$DIGEST_DIR"/*)
test "${#digests[@]}" -eq 2
args=()
while IFS= read -r tag; do
  test -z "$tag" || args+=(--tag "$tag")
done <<< "$IMAGE_TAGS"
test "${#args[@]}" -gt 0
for file in "${digests[@]}"; do
  digest="${file##*/}"
  [[ "$digest" =~ ^[a-f0-9]{64}$ ]]
  args+=("$IMAGE@sha256:$digest")
done
docker buildx imagetools create "${args[@]}"
first_tag="${IMAGE_TAGS%%$'\n'*}"
docker buildx imagetools inspect --raw "$first_tag" | jq -e '[.manifests[].platform | select(.os == "linux") | .architecture] | unique | sort == ["amd64", "arm64"]'
