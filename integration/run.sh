#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'Packaged integration requires a Linux/amd64 host with a local Docker daemon.' >&2
  exit 1
fi
if [[ $(id -u) != 0 ]]; then sudo -n true; fi
command -v nsenter >/dev/null
python3 -B -c 'import os, signal; assert signal.pidfd_send_signal; os.close(os.pidfd_open(os.getpid()))'
case $(docker info --format '{{.OSType}}/{{.Architecture}}') in
  linux/x86_64|linux/amd64) ;;
  *) echo 'Docker must run Linux/amd64.' >&2; exit 1 ;;
esac
if [[ ${1:-} == --check-host ]]; then exit 0; fi
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
  for network in "$project" "$project-front" "$project-private"; do
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
  -addext 'subjectAltName=DNS:mockery.ferretlang.org,DNS:forbidden.test,DNS:outside-proxy.test' \
  -keyout "$FIXTURE_TLS/key.pem" -out "$FIXTURE_TLS/cert.pem" 2>/dev/null
chmod 755 "$FIXTURE_TLS"
chmod 444 "$FIXTURE_TLS/key.pem" "$FIXTURE_TLS/cert.pem"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$FIXTURE_TLS/browser-state" ./integration/browserstate
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
python3 -B integration/startup.py "$FIXTURE_TLS" "$image" "$startup"
docker network create --internal --subnet 185.199.108.144/28 "$project" >/dev/null
docker network create --internal --ipv6 --subnet 172.30.247.0/24 --subnet fd42:5047::/64 "$project-private" >/dev/null
docker network create "$project-front" >/dev/null
docker run --detach --pull never --platform linux/amd64 --name "$PLAYGROUND_FIXTURE_CONTAINER" \
  --network "$project" --ip 185.199.108.153 --user 65532:65532 --read-only --cap-drop ALL \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  --env PLAYGROUND_CDP_PEER=185.199.108.154 \
  --volume "$PWD/integration/fixture.py:/fixture.py:ro" --volume "$FIXTURE_TLS:/tls:ro" \
  --entrypoint python python:3.13-alpine -B /fixture.py >/dev/null
docker network connect --ip 172.30.247.10 --ip6 fd42:5047::10 "$project-private" "$PLAYGROUND_FIXTURE_CONTAINER"
docker exec -i "$PLAYGROUND_FIXTURE_CONTAINER" python -B - <<'PY'
import time
import urllib.error
import urllib.request

deadline = time.monotonic() + 10
while True:
    try:
        with urllib.request.urlopen("http://127.0.0.1:9000/wait/cdp-observations", timeout=2) as reply:
            assert reply.read() == b"observed"
        break
    except (urllib.error.URLError, TimeoutError):
        assert time.monotonic() < deadline, "fixture never started watching the parent CDP port"
        time.sleep(0.05)
PY
printf 'nameserver 185.199.108.153\noptions ndots:1 attempts:1 timeout:1\n' > "$FIXTURE_TLS/resolv.conf"
bash deploy/container/run.sh "$image" "$PLAYGROUND_API_CONTAINER" 0 \
  --network "$project-front" --network "name=$project,ip=185.199.108.154" --network "$project-private" \
  --volume "$FIXTURE_TLS/resolv.conf:/etc/resolv.conf:ro" \
  --volume "$FIXTURE_TLS/browser-state:/usr/local/bin/browser-state:ro" \
  --env "PLAYGROUND_FIXTURE_SPKI=$spki" \
  --env PLAYGROUND_MAX_EXECUTION_TIMEOUT=60s --env PLAYGROUND_WRITE_TIMEOUT=65s >/dev/null
python3 -B integration/check.py
