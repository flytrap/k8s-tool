#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
target_user=${SUDO_USER:-root}
target_home=$(getent passwd "$target_user" | cut -d: -f6)
[[ -n $target_home && $target_home == /* ]] || fail 'Cannot determine kubectl user home'
install -d -m 0700 -o "$target_user" "$target_home/.kube"
install -m 0600 -o "$target_user" /etc/kubernetes/admin.conf "$target_home/.kube/config"
# Engine commands use sudo kubectl as well.
install -d -m 0700 /root/.kube
install -m 0600 /etc/kubernetes/admin.conf /root/.kube/config
