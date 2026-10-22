#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
export KUBECONFIG=/etc/kubernetes/admin.conf
platform
validate_version "$1"
workdir
archive="istio-$1-linux-$ARCH.tar.gz"
archive_path="$(dirname "$0")/$archive"
[[ -r $archive_path && -r $archive_path.sha256 ]] || fail "Bundled Istio $1 package for $ARCH is missing"
verify_sha256 "$archive_path" "$archive_path.sha256"
tar -xzf "$archive_path" -C "$WORK"
install -m 0755 "$WORK/istio-$1/bin/istioctl" /usr/local/bin/istioctl
istioctl install -f "$(dirname "$0")/conf/loki/iop.yaml" --set profile=default --set hub=docker.io/istio --set tag="$1" --skip-confirmation
# Match the release addons; retain the existing Loki/OpenTelemetry logging pipeline.
for addon in prometheus grafana kiali; do
  manifest="$WORK/istio-$1/samples/addons/$addon.yaml"
  [[ -f $manifest ]] || fail "Missing release addon: $addon"
  kubectl apply -f "$manifest"
done

source "$(dirname "$0")/loki-storage.sh"
mode=$(loki_storage_mode)
render_loki_manifest "$mode" "$(dirname "$0")/conf/loki/loki.yaml" "$WORK/loki.yaml"
if [[ $mode == ephemeral ]]; then printf '%s\n' 'Loki uses ephemeral emptyDir storage; logs are lost when its Pod is replaced.' >&2; fi
kubectl apply -n istio-system -f "$WORK/loki.yaml"
kubectl apply -n istio-system -f "$(dirname "$0")/conf/loki/otel.yaml"
kubectl apply -f "$(dirname "$0")/conf/loki/telemetry.yaml"
kubectl label namespace default istio-injection=enabled --overwrite
