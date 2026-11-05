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
cat > "$FIXTURE_TLS/namespace.json" <<'EOF'
{"defaultAction":"SCMP_ACT_ALLOW","syscalls":[{"names":["clone"],"action":"SCMP_ACT_ERRNO","errnoRet":1,"args":[{"index":0,"value":268435456,"valueTwo":268435456,"op":"SCMP_CMP_MASKED_EQ"}]},{"names":["clone3"],"action":"SCMP_ACT_ERRNO","errnoRet":38}]}
EOF
# Every denial exercises the ordinary image entrypoint, including partial rollback.
for denied in namespace:0:namespace_start_failed route:0:network_interface_enumeration_failed netfilter:12:namespace_policy_unavailable; do
  IFS=: read -r label protocol expected <<< "$denied"
  python3 - "$FIXTURE_TLS" "$label" "$protocol" "$expected" "$image" "$startup" <<'PY'
import json
from pathlib import Path
import subprocess
import sys

directory, label, protocol, expected, image, name = sys.argv[1:]
profile = Path(directory) / (label + ".json")
if label != "namespace":
    profile.write_text(json.dumps({
        "defaultAction": "SCMP_ACT_ALLOW",
        "syscalls": [{"names": ["socket"], "action": "SCMP_ACT_ERRNO", "errnoRet": 1,
                  "args": [{"index": 0, "value": 16, "op": "SCMP_CMP_EQ"},
                           {"index": 2, "value": int(protocol), "op": "SCMP_CMP_EQ"}]}],
    }))
log = Path(directory) / (label + ".log")
with log.open("w") as output:
    result = subprocess.run(["docker", "run", "--name", name, "--pull", "never", "--platform", "linux/amd64",
                             "--security-opt", "no-new-privileges:true", "--security-opt", "seccomp=" + str(profile), image],
                            stdout=output, stderr=subprocess.STDOUT, timeout=55)
assert result.returncode != 0 and expected in log.read_text(), log.read_text()
assert "Service listening" not in log.read_text(), "HTTP opened after isolation failure"
state = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .State}}", name], text=True))
assert state["Pid"] == 0 and state.get("Health", {}).get("Status") != "healthy", state
subprocess.run(["docker", "rm", "--volumes", name], check=True, stdout=subprocess.DEVNULL)
print(f"Startup failed closed with {label} sockets denied", flush=True)
PY
done
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
python3 -B integration/check.py
