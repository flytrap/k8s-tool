#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
export KUBECONFIG=/etc/kubernetes/admin.conf
[[ $# == 4 && $1 =~ ^[a-zA-Z0-9.-]+$ && $2 =~ ^/[a-zA-Z0-9/_.-]*$ && $3 =~ ^[a-z0-9][a-z0-9-]*$ ]] || fail 'Usage: install.sh server absolutePath namespace chartVersion'
validate_version "$4"
helm repo add nfs-subdir-external-provisioner https://kubernetes-sigs.github.io/nfs-subdir-external-provisioner/ --force-update
helm repo update nfs-subdir-external-provisioner
helm upgrade --install nfs-subdir-external-provisioner nfs-subdir-external-provisioner/nfs-subdir-external-provisioner --version "$4" --namespace "$3" --create-namespace --set-string "nfs.server=$1" --set-string "nfs.path=$2" --set storageClass.name=nfs-storage --set storageClass.defaultClass=true --set storageClass.archiveOnDelete=false --wait --timeout 5m
