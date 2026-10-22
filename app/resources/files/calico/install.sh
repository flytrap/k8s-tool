#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
export KUBECONFIG=/etc/kubernetes/admin.conf
validate_version "$1"
workdir
manifest="$(dirname "$0")/calico.yaml"
[[ -r $manifest ]] || fail 'Bundled Calico manifest is missing'
grep -Fq "image: quay.io/calico/node:$1" "$manifest" || fail "Bundled Calico manifest does not match requested version $1"
cp "$manifest" "$WORK/calico.yaml"
# Explicitly set the pod CIDR; the manifest's default is commented out.
sed -i '/# - name: CALICO_IPV4POOL_CIDR/{s/# -/-/;n;s/#   value: "192.168.0.0\/16"/  value: "10.244.0.0\/16"/;}' "$WORK/calico.yaml"
grep -q 'value: "10.244.0.0/16"' "$WORK/calico.yaml" || fail 'Calico manifest CIDR format changed'
kubectl apply --server-side --force-conflicts -f "$WORK/calico.yaml"
kubectl rollout status daemonset/calico-node -n kube-system --timeout=300s
