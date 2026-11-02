#!/bin/sh
set -eu
proxy=$(getent hosts egress | awk 'NR==1 { print $1 }')
# Resolve once under the trusted init identity. Browser DNS (including Docker's
# loopback resolver) is deliberately unavailable after these rules are loaded.
case "$proxy" in ''|*[!0-9.]*) exit 1;; esac
# One atomic transaction. Also support starting the job again in the same netns.
nft -f - <<RULES
add table inet playground
flush table inet playground
add chain inet playground output { type filter hook output priority -150; policy accept; }
add rule inet playground output meta skuid 10001 ip daddr $proxy tcp dport 3128 accept
add rule inet playground output meta skuid 10001 ip daddr 127.0.0.1 tcp sport 9223 ct direction reply ct state established accept
add rule inet playground output meta skuid 10001 drop
RULES
printf '%s\n' "$proxy" > /policy/proxy-ip
