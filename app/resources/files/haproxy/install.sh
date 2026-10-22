#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
[[ $# -gt 0 ]] || fail 'HAProxy requires at least one backend'
for backend in "$@"; do
  [[ $backend =~ ^server\ [a-zA-Z0-9._-]+\ [0-9.]+:6443\ check$ ]] || fail "Invalid HAProxy backend: $backend"
done
apt-get update
apt_install haproxy
workdir
cat > "$WORK/haproxy.cfg" <<'EOF'
global
  log /dev/log local0
  user haproxy
  group haproxy
  daemon
defaults
  mode tcp
  log global
  timeout connect 10s
  timeout client 1m
  timeout server 1m
frontend kubernetes
  bind *:16443
  default_backend controlplanes
backend controlplanes
  balance roundrobin
  option tcp-check
EOF
printf '  %s\n' "$@" >> "$WORK/haproxy.cfg"
haproxy -c -f "$WORK/haproxy.cfg"
install -m 0644 "$WORK/haproxy.cfg" /etc/haproxy/haproxy.cfg.new
mv /etc/haproxy/haproxy.cfg.new /etc/haproxy/haproxy.cfg
systemctl enable haproxy
systemctl restart haproxy
