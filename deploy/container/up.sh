#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
docker build --platform linux/amd64 -t playground-api:local .
bash deploy/container/run.sh playground-api:local playground "${PLAYGROUND_PORT:-8080}"
for attempt in {1..90}; do
  state=$(docker inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' playground)
  case "$state" in
    'running healthy') exit 0 ;;
    exited*) docker logs playground; exit 1 ;;
  esac
  sleep 1
done
docker logs playground
exit 1
