# K8s tool

Install Kubernetes online over SSH with kubeadm and containerd.

## Components and versions

| Component | Version |
| --- | --- |
| Kubernetes (kubeadm / kubelet / kubectl) | `v1.37.1` |
| containerd / runc / CNI plugins | `2.3.6` / `1.4.2` / `1.8.0` |
| Istio CLI / control plane / proxy | `1.31.2` |
| Calico | `v3.33.0` |
| Helm | `v3.21.3` |
| Metrics Server | `v0.8.1` |
| kube-state-metrics Helm chart | `8.6.0` |
| NFS provisioner Helm chart | `4.0.18` |

Kubernetes and Istio versions come only from `kubernetes-version` and
`istio-version` in `config.yaml`. Both fields are required in each config file, and values are
passed to package installation, kubeadm initialization, image pulls, expansion
checks and Istio installation. They must name stable
`v1.x.y` and `1.x.y` releases. Other component pins are defined in
[app/resources/resources.go](app/resources/resources.go). Missing releases fail
without falling back to another version. Containerd and kubelet use systemd
cgroups and `unix:///run/containerd/containerd.sock`.
HAProxy, Keepalived, Chrony and NFS client utilities use online OS repositories.

The current [Istio support matrix](https://istio.io/latest/docs/releases/supported-releases/)
and [Calico 3.33 requirements](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements)
include Kubernetes 1.37.

## Online installation requirements

- Ubuntu or Debian targets with systemd, amd64 or arm64. Calico 3.33 requires Linux kernel 5.10 or newer.
- Access to OS mirrors, `pkgs.k8s.io`, GitHub / `raw.githubusercontent.com`, `get.helm.sh`, and the Helm repositories used by addons.
- The Calico 3.33.0 YAML manifest is bundled with the project and is not fetched from `raw.githubusercontent.com` during installation; nodes still need access to image registries.
- The Metrics Server `v0.8.1` manifest and kube-state-metrics `8.6.0` Helm chart are bundled. Step 14 does not access GitHub or the Prometheus Helm repository; images must still be pulled online or preloaded.
- Registry access from nodes, including `registry.k8s.io`, `quay.io` and `docker.io`.
- Root SSH access or a sudo-capable user. Initialization configures passwordless sudo for the deployment user.
- Configured VIP, node identities and credentials, plus trusted host keys or an explicit verification bypass.
- Firewall rules allowing Kubernetes, Calico and HA traffic. Scripts do not disable the firewall automatically.

Scripts and templates are embedded in the executable. No `resource.zip` or local
`resource` directory is required. Istio 1.31.2 Linux amd64 and arm64 archives are
also embedded by architecture and SHA256-verified; other binary downloads verify
published SHA256 files, and packages use signed repositories.

## Usage

```bash
k8s-tool help
k8s-tool install --steps
k8s-tool install --config config.yaml
k8s-tool install --config config.yaml --step 4,5,6
k8s-tool install --update --steps
k8s-tool install --update --config config.yaml
```

The default file is `config.yaml` in the current directory. Installation steps
4–6 now install containerd, install Kubernetes tools, and pull Kubernetes images.
Expansion steps 5–7 perform those operations. Compared with the old offline
workflow, tools must be installed before image pulling; check `--steps` again
before selecting numbers. Partial execution assumes earlier dependencies are
complete. Connection is always prepended; expansion also prepends discovery.
Image step 6 checks containerd's `k8s.io` namespace and skips exact image
references already present; it pulls only missing Kubernetes images. Preloaded
images must use the references printed by `kubeadm config images list`.

For offline Istio image bundles, see the
[Istio offline image guide](docs/istio-offline-images.zh.md).
For offline Metrics Server and kube-state-metrics image bundles, see the
[application offline assets guide](docs/app-offline-assets.zh.md).

## Topology and expansion

Control-plane nodes must also carry the `etcd` role. Independent etcd topology
is unsupported. Hostnames must be unique lowercase DNS names; node addresses and
VIPs must be IPv4 literals. Include every existing control-plane and all new nodes
in the expansion configuration. Management-node discovery is independent of
configuration order.

`--update` expands an existing **Kubernetes v1.37.1 / containerd 2.x** cluster.
It does not upgrade across Kubernetes releases or migrate Docker workloads.
API server, kubelet and runtime versions are checked before initializing new
nodes. Upgrade old v1.29.2 / Docker clusters with a separate staged upgrade,
drain and runtime migration procedure; scripts do not uninstall Docker or
overwrite nodes that have already joined a cluster.

Update step 9 joins nodes and step 10 synchronizes HAProxy backends and
Keepalived peers. Resume with `--update --step 9,10`, or retry step 10 alone after
joining. Running peers are refreshed before additional participants start. If no
HA instance is running, restore the existing VIP first. Leave `region` empty for
a LAN VIP; setting an AWS region enables online AWS CLI installation and EC2
private-IP reassignment using instance-role credentials.

## SSH verification and timeouts

Verification defaults to the current user's `~/.ssh/known_hosts`. Alternatively,
set a node's `host-key-fingerprint` to a verified SHA256 fingerprint. An empty
`ssh.known-hosts` uses the default file.

```bash
k8s-tool install --config config.yaml --insecure-skip-host-key-check
# Explicit false overrides the configuration and restores verification.
k8s-tool install --config config.yaml --insecure-skip-host-key-check=false
```

`ssh.insecure-skip-host-key-check` defaults to `false`; enabling it skips both
known_hosts and SHA256 fingerprint checks. Explicit CLI values override the file.
Connection, command and transfer timeouts default to 30 seconds, 15 minutes and
30 minutes. Configure positive durations under `ssh`. Cancellation or timeout
closes the affected connection; it does not roll back remote changes. Inspect a
failed node before retrying.

## Monitoring and storage

Istio uses Prometheus, Grafana and Kiali examples from the same release archive.
The existing Loki 2.7.3 / OpenTelemetry Collector 0.73.0 logging pipeline is
retained with online image pulls; those logging versions are not upgraded here.
New Loki deployments use a PVC when a default StorageClass exists; otherwise they
use `emptyDir`, so replacing the Pod loses its logs. Existing Loki deployments
retain their storage type. Telemetry uses `telemetry.istio.io/v1`. Metrics Server preserves the previous
`--kubelet-insecure-tls` behavior; production deployments should provision trusted
kubelet serving certificates and remove that flag.

An empty NTP server skips Chrony. An empty NFS server skips the provisioner but
still installs NFS client utilities. Configured NFS uses a pinned online Helm
chart. Local regression fixtures do not validate real installation, networking or
VIP failover.

## Build

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/k8s-tool-linux .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o build/k8s-tool-darwin .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/k8s-tool.exe .
```

## Default configuration

The root [config.yaml](config.yaml) is the default example. Replace its addresses
and credentials before installation. Username/password are base64-encoded;
setting `keyPath` selects private-key authentication.

```yaml
# Example deployment configuration. Replace node addresses and credentials before use.
namespace: default
registry: registry.k8s.io
# Select Kubernetes and Istio releases for this deployment.
kubernetes-version: v1.37.1
istio-version: 1.31.2
cri-socket: unix:///run/containerd/containerd.sock

vip: 192.168.56.151
# Empty uses a LAN VIP; set an AWS region to enable EC2 private-IP reassignment.
region: ""

ssh:
  # false: verify known_hosts or the node's SHA256 fingerprint.
  # true: skip both checks. An explicit CLI value overrides this setting.
  insecure-skip-host-key-check: false
  # Empty uses the current user's ~/.ssh/known_hosts.
  known-hosts: ""
  connect-timeout: 30s
  command-timeout: 15m
  transfer-timeout: 30m

ntp:
  # Empty disables Chrony installation.
  server: ""
  allow: ""
  timezone: Asia/Shanghai

nodes:
  - address: 192.168.56.101
    hostname: master1
    role: [etcd, controlplane, worker]
    port: 22
    # Example only: both values encode "deploy" using base64.
    username: ZGVwbG95
    password: ZGVwbG95
    # A non-empty keyPath selects private-key authentication instead of password.
    keyPath: ""
    # Optional alternative to known_hosts: SHA256:<verified server fingerprint>.
    host-key-fingerprint: ""

nfs:
  # Empty disables the NFS provisioner; NFS client utilities are still installed.
  server: ""
  path: ""
```
