#!/bin/sh
set -eu
proxy=$(cat /policy/proxy-ip)
# Chrome, including net and renderer children, has only this UID's egress.
# Keep browser stderr private: Chromium can print submitted URLs and page text.
exec chrome --headless=new --user-data-dir=/tmp/profile --no-first-run \
  --remote-debugging-port=9223 --remote-debugging-address=127.0.0.1 \
  --proxy-server="http://$proxy:3128" '--proxy-bypass-list=<-loopback>' \
  --disable-quic --force-webrtc-ip-handling-policy=disable_non_proxied_udp \
  --disable-extensions --disable-background-networking --disable-component-update \
  --disable-background-timer-throttling --disable-backgrounding-occluded-windows \
  --disable-renderer-backgrounding --disable-gpu --disable-breakpad --no-default-browser-check \
  "$@" about:blank >/dev/null 2>&1
