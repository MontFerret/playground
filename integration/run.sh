#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
project="playground-test-$$"
export PLAYGROUND_API_CONTAINER="$project-api"
export PLAYGROUND_FIXTURE_CONTAINER="$project-fixture"
startup="$project-startup"
FIXTURE_TLS=$(mktemp -d "${TMPDIR:-/tmp}/playground-fixture.XXXXXX")
cleanup() {
  result=$?
  trap - EXIT
  if [[ $result != 0 ]]; then
    docker logs "$PLAYGROUND_API_CONTAINER" || true
    docker logs "$PLAYGROUND_FIXTURE_CONTAINER" || true
  fi
  for container in "$PLAYGROUND_API_CONTAINER" "$PLAYGROUND_FIXTURE_CONTAINER" "$startup"; do
    if docker inspect "$container" >/dev/null 2>&1; then
      docker rm --force --volumes "$container" >/dev/null || result=1
    fi
  done
  for network in "$project" "$project-front"; do
    if docker network inspect "$network" >/dev/null 2>&1; then
      docker network rm "$network" >/dev/null || result=1
    fi
  done
  rm -rf "$FIXTURE_TLS"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=mockery.ferretlang.org \
  -addext 'subjectAltName=DNS:mockery.ferretlang.org,DNS:forbidden.test' \
  -keyout "$FIXTURE_TLS/key.pem" -out "$FIXTURE_TLS/cert.pem" 2>/dev/null
chmod 755 "$FIXTURE_TLS"
chmod 444 "$FIXTURE_TLS/key.pem" "$FIXTURE_TLS/cert.pem"
spki=$(openssl x509 -in "$FIXTURE_TLS/cert.pem" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl base64 -A)
image=${PLAYGROUND_API_IMAGE:-playground-api:local}
if [[ "${PLAYGROUND_USE_CACHED_IMAGES:-0}" != 1 ]]; then
  if ! docker image inspect python:3.13-alpine >/dev/null 2>&1; then
    docker pull --platform linux/amd64 python:3.13-alpine
  fi
  if [[ -z "${PLAYGROUND_API_IMAGE:-}" ]]; then
    docker build --platform linux/amd64 -t "$image" .
  fi
fi
# Deny only namespace creation. Ordinary Go threads and the proxy process can
# start, so this exercises rollback after partial initialization.
cat > "$FIXTURE_TLS/deny-namespace.json" <<'EOF'
{"defaultAction":"SCMP_ACT_ALLOW","syscalls":[{"names":["clone"],"action":"SCMP_ACT_ERRNO","errnoRet":1,"args":[{"index":0,"value":268435456,"valueTwo":268435456,"op":"SCMP_CMP_MASKED_EQ"}]},{"names":["clone3"],"action":"SCMP_ACT_ERRNO","errnoRet":38}]}
EOF
if docker run --name "$startup" --pull never --platform linux/amd64 \
  --security-opt "seccomp=$FIXTURE_TLS/deny-namespace.json" "$image" probe > "$FIXTURE_TLS/startup.log" 2>&1; then
  echo 'Namespace denial did not fail closed' >&2
  exit 1
fi
grep -q namespace_start_failed "$FIXTURE_TLS/startup.log"
test "$(docker inspect --format '{{.State.Pid}}' "$startup")" = 0
docker rm --volumes "$startup" >/dev/null
docker network create --internal --subnet 185.199.108.144/28 "$project" >/dev/null
docker network create "$project-front" >/dev/null
docker run --detach --pull never --platform linux/amd64 --name "$PLAYGROUND_FIXTURE_CONTAINER" \
  --network "$project" --ip 185.199.108.153 --user 65532:65532 --read-only --cap-drop ALL \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  --volume "$PWD/integration/fixture.py:/fixture.py:ro" --volume "$FIXTURE_TLS:/tls:ro" \
  --entrypoint python python:3.13-alpine -B /fixture.py >/dev/null
bash deploy/container/run.sh "$image" "$PLAYGROUND_API_CONTAINER" 0 \
  --network "$project-front" --network "$project" --add-host mockery.ferretlang.org:185.199.108.153 \
  --add-host forbidden.test:185.199.108.153 \
  --env "PLAYGROUND_FIXTURE_SPKI=$spki" \
  --env PLAYGROUND_MAX_EXECUTION_TIMEOUT=30s --env PLAYGROUND_WRITE_TIMEOUT=35s >/dev/null
python3 integration/check.py
