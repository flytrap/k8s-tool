#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
[[ ! -e /etc/kubernetes/kubelet.conf ]] || fail 'Existing Kubernetes node; upgrade requires a separate procedure'
validate_version "$1"
version=${1#v}
minor=${version%.*}
workdir
mkdir -p /etc/apt/keyrings
download "https://pkgs.k8s.io/core:/stable:/v$minor/deb/Release.key" "$WORK/Release.key"
gpg --batch --yes --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg "$WORK/Release.key"
printf 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v%s/deb/ /\n' "$minor" > /etc/apt/sources.list.d/kubernetes.list
apt-get update
# Resolve only the requested upstream release; reject missing/ambiguous builds.
for package in kubelet kubeadm kubectl; do
  pin=$(exact_apt_package_version "$package" "$version")
  [[ -n $pin ]] || fail "Unavailable exact package: $package $version"
  apt_install "$package=$pin"
done
apt-mark hold kubelet kubeadm kubectl
systemctl enable kubelet
