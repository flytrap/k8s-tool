#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
export KUBECONFIG=/etc/kubernetes/admin.conf
validate_version "$1"
validate_version "$2"
workdir
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
kubectl apply -f "$script_dir/metrics-server.yaml"
# Preserve the previous deployment's self-signed kubelet certificate behavior.
kubectl patch deployment metrics-server -n kube-system --type=strategic -p '{"spec":{"template":{"spec":{"containers":[{"name":"metrics-server","args":["--cert-dir=/tmp","--secure-port=10250","--kubelet-preferred-address-types=InternalIP,ExternalIP,Hostname","--kubelet-use-node-status-port","--metric-resolution=15s","--kubelet-insecure-tls"]}]}}}}'
chart="$script_dir/charts/kube-state-metrics-$2.tgz"
verify_sha256 "$chart" "$chart.sha256"
helm upgrade --install kube-state-metrics "$chart" --namespace kube-system --wait --timeout 5m
kubectl rollout status deployment/metrics-server -n kube-system --timeout=300s
