#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
[[ $# == 3 && $1 =~ ^[a-zA-Z0-9.-]+$ && ( -z $2 || $2 =~ ^[0-9./]+$ ) && $3 =~ ^[a-zA-Z0-9_/+-]+$ ]] || fail 'Invalid chrony parameters'
apt-get update
apt_install chrony tzdata
workdir
cat > "$WORK/chrony.conf" <<EOF
server $1 iburst
driftfile /var/lib/chrony/chrony.drift
makestep 1.0 3
rtcsync
EOF
if [[ -n $2 ]]; then printf 'allow %s\nlocal stratum 10\n' "$2" >> "$WORK/chrony.conf"; fi
install -m 0644 "$WORK/chrony.conf" /etc/chrony/chrony.conf
timedatectl set-timezone "$3"
systemctl enable chrony
systemctl restart chrony
