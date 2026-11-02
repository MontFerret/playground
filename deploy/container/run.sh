#!/usr/bin/env bash
set -euo pipefail
image=$1 name=$2 port=$3
shift 3
# Identity-management capabilities are a subset of Docker's defaults. Network
# administration exists only inside the newly created user/network namespace.
exec docker run --detach --pull never --platform linux/amd64 --name "$name" \
  --read-only --tmpfs /tmp:rw,exec,nosuid,nodev,size=1073741824 \
  --security-opt no-new-privileges:true --security-opt seccomp=deploy/browser/seccomp.json \
  --cap-drop ALL --cap-add SETUID --cap-add SETGID --cap-add CHOWN --cap-add KILL --cap-add DAC_OVERRIDE \
  --cpus 2 --memory 2g --shm-size 64m --stop-timeout 10 \
  --publish "127.0.0.1:$port:8080" "$@" "$image"
