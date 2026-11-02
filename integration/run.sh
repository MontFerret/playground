#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME="playground-test-$$"
export COMPOSE_FILE="compose.yaml:integration/compose.yaml"
export PLAYGROUND_PORT=0
FIXTURE_TLS=$(mktemp -d "${TMPDIR:-/tmp}/playground-fixture.XXXXXX")
export FIXTURE_TLS
cleanup() {
  status=$?
  trap - EXIT
  if [[ $status != 0 ]]; then
    docker compose logs --no-color api fixture firewall || true
  fi
  docker compose down -v --remove-orphans --timeout 8 || status=1
  rm -rf "$FIXTURE_TLS"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Ephemeral fixture-only key, readable by its non-root container.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=mockery.ferretlang.org \
  -addext 'subjectAltName=DNS:mockery.ferretlang.org,DNS:forbidden.test' \
  -keyout "$FIXTURE_TLS/key.pem" -out "$FIXTURE_TLS/cert.pem" 2>/dev/null
chmod 755 "$FIXTURE_TLS"
chmod 444 "$FIXTURE_TLS/key.pem" "$FIXTURE_TLS/cert.pem"
FIXTURE_SPKI=$(openssl x509 -in "$FIXTURE_TLS/cert.pem" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl base64 -A)
export FIXTURE_SPKI
if [[ "${PLAYGROUND_USE_CACHED_IMAGES:-0}" != 1 ]]; then
  docker compose build egress firewall relay
  if [[ -z "${PLAYGROUND_API_IMAGE:-}" ]]; then docker compose build api; fi
fi
docker compose up --no-build --pull never --wait --wait-timeout 90
python3 integration/check.py
