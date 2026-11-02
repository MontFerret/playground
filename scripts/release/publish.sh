#!/usr/bin/env bash
# Re-runnable with the verified artifacts recovered into dist/.
set -euo pipefail
cd "$(dirname "$0")/../.."
descriptor=dist/image.json
archive=dist/playground.oci.tar
jq -e '.verified == true and (.channel == "stable" or .channel == "prerelease")' "$descriptor" >/dev/null
version=$(jq -er .version "$descriptor")
public=$(jq -er .public "$descriptor")
private=$(jq -er .private "$descriptor")
minor=$(jq -er .minor "$descriptor")
channel=$(jq -er .channel "$descriptor")
digest=$(jq -er .digest "$descriptor")
[[ "$public" == ghcr.io/montferret/playground ]]
[[ "$private" == us-east1-docker.pkg.dev/montferret/playground/playground ]]
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
skopeo inspect --raw "oci-archive:$archive" > "$dir/raw"
[[ $(skopeo manifest-digest "$dir/raw") == "$digest" ]]
remote_digest() {
  skopeo inspect "$@" --raw "docker://$ref" > "$dir/raw" && skopeo manifest-digest "$dir/raw"
}
# GHCR can deny the pull-token request for a package that does not exist yet.
# Confirm package absence with the same scoped GitHub token before first push.
missing_github_package() {
  [[ "$repo" == "$public" && -n "${GH_TOKEN:-}" ]] || return 1
  status=$(curl -sS --connect-timeout 5 --max-time 15 --output "$dir/package" --write-out '%{http_code}' \
    --header "Authorization: Bearer $GH_TOKEN" --header 'Accept: application/vnd.github+json' \
    https://api.github.com/orgs/montferret/packages/container/playground) || return 1
  [[ "$status" == 404 ]]
}
for repo in "$public" "$private"; do
  ref="$repo:$version"
  if existing=$(remote_digest 2>"$dir/error"); then
    [[ "$existing" == "$digest" ]] || { echo "Conflicting version at $ref" >&2; exit 1; }
  else
    # Never overwrite a version after an ambiguous auth/network failure.
    if ! grep -Eiq 'manifest unknown|name unknown|manifest_unknown|name_unknown|not found|404' "$dir/error"; then
      if ! grep -Eiq 'denied|unauthorized|forbidden|401|403' "$dir/error" || ! missing_github_package; then
        echo "Registry inspection failed for $ref" >&2
        exit 1
      fi
    fi
    skopeo copy --preserve-digests "oci-archive:$archive" "docker://$ref"
  fi
done
for repo in "$public" "$private"; do
  ref="$repo:$version"
  [[ $(remote_digest) == "$digest" ]]
done
skopeo copy --src-no-creds "docker://$public@$digest" "dir:$dir/public"
skopeo copy "docker://$private@$digest" "dir:$dir/private"
if skopeo copy --src-no-creds "docker://$private@$digest" "dir:$dir/anonymous" 2>"$dir/error"; then
  echo "Private image allowed an anonymous pull" >&2; exit 1
fi
grep -Eiq 'denied|unauthorized|unauthenticated|forbidden|401|403' "$dir/error"
if [[ "$channel" == stable ]]; then
  for alias in "$minor" latest; do
    for repo in "$public" "$private"; do
      ref="$repo:$alias"
      skopeo copy --preserve-digests "docker://$repo@$digest" "docker://$ref"
      [[ $(remote_digest) == "$digest" ]]
    done
  done
fi
