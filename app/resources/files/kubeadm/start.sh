#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
[[ $# == 6 ]] || fail 'Usage: start.sh VIP hostname IP CRISocket K8sVersion ImageRepository'
validate_ip "$1"
validate_ip "$3"
validate_version "$5"
[[ $2 =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ && $4 =~ ^unix:///[a-zA-Z0-9/_.-]+$ && $6 =~ ^[a-zA-Z0-9][a-zA-Z0-9/_.:-]*$ ]] || fail 'Invalid kubeadm parameters'
cat > kubeadm-config.yaml <<EOF
apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: "$3"
  bindPort: 6443
nodeRegistration:
  name: "$2"
  criSocket: "$4"
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
kubernetesVersion: "$5"
controlPlaneEndpoint: "$1:16443"
imageRepository: "$6"
networking:
  podSubnet: "10.244.0.0/16"
apiServer:
  certSANs:
    - "$1"
    - "$3"
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
EOF
kubeadm init --config kubeadm-config.yaml --upload-certs
