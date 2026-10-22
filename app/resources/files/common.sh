#!/usr/bin/env bash
set -euo pipefail
fail() { printf '%s\n' "$*" >&2; exit 1; }
require_root() {
  if [[ $EUID -ne 0 ]]; then exec sudo -n bash "$0" "$@"; fi
}
platform() {
  [[ -r /etc/os-release ]] || fail 'Unsupported OS: Ubuntu or Debian required'
  . /etc/os-release
  case "$ID" in ubuntu|debian) ;; *) fail "Unsupported OS: $ID (Ubuntu or Debian required)";; esac
  case "$(uname -m)" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) fail 'Unsupported architecture';; esac
}
apt_install() { DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"; }
exact_apt_package_version() {
  apt-cache madison "$1" | awk -v version="$2" 'index($3, version "-")==1 && substr($3,length(version)+2) ~ /^[0-9.]+$/ {if (pin == "") pin=$3} END {if (pin != "") print pin}'
}
download() { [[ $1 == https://* ]] || fail 'Downloads require HTTPS'; curl --fail --show-error --silent --location --retry 3 --proto '=https' --proto-redir '=https' "$1" -o "$2"; }
# Accept upstream plain hash and sha256sum formats; verify the selected artifact only.
verify_sha256() {
  local artifact=$1 sums=$2 hash
  hash=$(awk -v name="$(basename "$artifact")" 'NF==1 && length($1)==64 {print $1; exit} $2==name || $2=="*"name {print $1; exit}' "$sums")
  [[ $hash =~ ^[a-fA-F0-9]{64}$ ]] || fail "No SHA256 for $(basename "$artifact")"
  printf '%s  %s\n' "$hash" "$artifact" | sha256sum -c - >/dev/null || fail "Checksum mismatch: $artifact"
}
checked_download() { download "$1" "$3"; download "$2" "$3.sha256"; verify_sha256 "$3" "$3.sha256"; }
workdir() { WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT; }
validate_version() { [[ $1 =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Expected an exact release version'; }
validate_ip() { [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Expected IPv4 address: $1"; local octet; IFS=. read -r -a octets <<< "$1"; for octet in "${octets[@]}"; do ((10#$octet <= 255)) || fail "Invalid IPv4 address: $1"; done; }
