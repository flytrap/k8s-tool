#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
validate_version "$1"
workdir
archive="helm-$1-linux-$ARCH.tar.gz"
url="https://get.helm.sh/$archive"
checked_download "$url" "$url.sha256sum" "$WORK/$archive"
tar -xzf "$WORK/$archive" -C "$WORK"
install -m 0755 "$WORK/linux-$ARCH/helm" /usr/local/bin/helm
