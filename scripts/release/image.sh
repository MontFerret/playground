#!/usr/bin/env bash
# GoReleaser supplies the binary, version, registries, and alias policy.
set -euo pipefail
binary=$1 version=$2 public=$3 private=$4 minor=$5 channel=$6
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cp "$binary" "$dir/playground"
cp Dockerfile.release "$dir/Dockerfile"
cp deploy/browser/policy.json "$dir/policy.json"
image="playground-release:$version"
docker build --platform linux/amd64 \
  --label org.opencontainers.image.source=https://github.com/MontFerret/playground \
  --label "org.opencontainers.image.version=$version" --label "org.opencontainers.image.revision=$(git rev-parse HEAD)" \
  -t "$image" "$dir"
docker save "$image" -o "$dir/built.tar"
archive=dist/playground.oci.tar
skopeo copy "docker-archive:$dir/built.tar" "oci-archive:$archive:playground"
skopeo inspect --raw "oci-archive:$archive" > "$dir/manifest"
digest=$(skopeo manifest-digest "$dir/manifest")
jq -n --arg public "$public" --arg private "$private" --arg version "$version" \
  --arg minor "$minor" --arg channel "$channel" --arg digest "$digest" \
  '{public:$public,private:$private,version:$version,minor:$minor,channel:$channel,digest:$digest}' > dist/image.json
# Import the archived artifact under a fresh tag for the single-container suite.
# docker-archive uses the selected Docker CLI context on Linux and Desktop alike.
tested="playground-verified:$version"
skopeo copy "oci-archive:$archive" "docker-archive:$dir/verified.tar:$tested"
docker load -i "$dir/verified.tar"
EXPECTED_SERVICE_VERSION="$version" PLAYGROUND_API_IMAGE="$tested" make integration

# Record verification only after the archive-backed suite has passed.
jq '.verified = true' dist/image.json > "$dir/verified.json"
mv "$dir/verified.json" dist/image.json
