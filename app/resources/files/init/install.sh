#!/usr/bin/env bash
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
  if [[ -n ${1:-} ]]; then printf '%s\n' "$(printf '%s' "$1" | base64 --decode)" | sudo -S bash "$0" "$@"; exit $?; fi
  exec sudo -n bash "$0" "$@"
fi
source "$(dirname "$0")/../common.sh"
platform
[[ ${2:-} =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$ ]] || fail 'Invalid hostname'
# A running cluster/runtime requires an explicit migration procedure.
[[ ! -e /etc/kubernetes/kubelet.conf && ! -S /var/run/docker.sock && ! -S /run/containerd/containerd.sock && ! -S /var/run/crio/crio.sock ]] || fail 'Existing Kubernetes/runtime detected; automatic migration is unsupported'
for unit in docker containerd crio; do
  if systemctl is-active --quiet "$unit"; then fail "Existing $unit runtime detected; automatic migration is unsupported"; fi
done
apt-get update
apt_install sudo ca-certificates curl gnupg apt-transport-https iproute2 conntrack socat ethtool util-linux openssl
if [[ -n ${SUDO_USER:-} && $SUDO_USER != root ]]; then
  [[ $SUDO_USER =~ ^[a-zA-Z_][a-zA-Z0-9_-]*$ ]] || fail 'Invalid sudo user'
  workdir
  printf '%s ALL=(ALL) NOPASSWD: ALL\n' "$SUDO_USER" > "$WORK/sudoers"
  visudo -cf "$WORK/sudoers"
  install -m 0440 "$WORK/sudoers" /etc/sudoers.d/k8s-tool
fi
hostnamectl set-hostname "$2"
swapoff -a
sed -i '/^[^#].*[[:space:]]swap[[:space:]]/s/^/#/' /etc/fstab
printf 'overlay\nbr_netfilter\n' > /etc/modules-load.d/k8s.conf
modprobe overlay
modprobe br_netfilter
cat > /etc/sysctl.d/99-k8s-tool.conf <<'EOF'
net.bridge.bridge-nf-call-iptables=1
net.bridge.bridge-nf-call-ip6tables=1
net.ipv4.ip_forward=1
EOF
sysctl --system
