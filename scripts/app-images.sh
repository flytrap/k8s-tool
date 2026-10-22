#!/usr/bin/env bash
set -euo pipefail

readonly namespace=k8s.io
readonly images=(
  registry.k8s.io/metrics-server/metrics-server:v0.8.1
  registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.20.0
)

usage() {
  cat <<'EOF'
Usage:
  app-images.sh list
  app-images.sh pull <amd64|arm64>
  app-images.sh export <amd64|arm64>
  app-images.sh import <archive.tar>

Run pull/export on a machine that can reach registry.k8s.io and has containerd
2.x ctr. Import the matching archive on every Kubernetes node.
EOF
}

run_ctr() {
  if [[ $EUID -eq 0 ]]; then
    ctr --namespace "$namespace" "$@"
  else
    sudo ctr --namespace "$namespace" "$@"
  fi
}

pull_images() {
  local arch=$1 image
  for image in "${images[@]}"; do
    run_ctr images pull --platform "linux/$arch" "$image"
  done
}

case ${1:-} in
  list)
    printf '%s\n' "${images[@]}"
    ;;
  pull)
    [[ $# == 2 && ( $2 == amd64 || $2 == arm64 ) ]] || { usage >&2; exit 2; }
    pull_images "$2"
    ;;
  export)
    [[ $# == 2 && ( $2 == amd64 || $2 == arm64 ) ]] || { usage >&2; exit 2; }
    pull_images "$2"
    archive="app-images-metrics-server-v0.8.1-kube-state-metrics-v2.20.0-linux-$2.tar"
    run_ctr images export --platform "linux/$2" "$archive" "${images[@]}"
    printf 'Created %s\n' "$archive"
    ;;
  import)
    [[ $# == 2 && -f $2 ]] || { usage >&2; exit 2; }
    run_ctr images import "$2"
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
