#!/usr/bin/env bash
set -euo pipefail

readonly namespace=k8s.io
readonly images=(
  docker.io/istio/pilot:1.31.2
  docker.io/istio/proxyv2:1.31.2
  docker.io/prom/prometheus:v3.10.0
  ghcr.io/prometheus-operator/prometheus-config-reloader:v0.89.0
  docker.io/grafana/grafana:12.0.1
  quay.io/kiali/kiali:v2.31
  docker.io/grafana/loki:2.7.3
  docker.io/otel/opentelemetry-collector-contrib:0.73.0
)

usage() {
  cat <<'EOF'
Usage:
  istio-images.sh list
  istio-images.sh pull <amd64|arm64>
  istio-images.sh export <amd64|arm64>
  istio-images.sh import <archive.tar>

Run pull/export on a machine that can access the image registries and has
containerd 2.x ctr. Import the matching archive on every Kubernetes node.
EOF
}

run_ctr() {
  if [[ $EUID -eq 0 ]]; then
    ctr --namespace "$namespace" "$@"
  else
    sudo ctr --namespace "$namespace" "$@"
  fi
}

command=${1:-}
pull_images() {
  local arch=$1 image
  for image in "${images[@]}"; do
    run_ctr images pull --platform "linux/$arch" "$image"
  done
}

case $command in
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
    archive="istio-images-1.31.2-linux-$2.tar"
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
