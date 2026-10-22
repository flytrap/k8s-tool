#!/usr/bin/env bash
source "$(dirname "$0")/../../common.sh"
require_root "$@"
platform
apt-get update
apt_install nfs-common
