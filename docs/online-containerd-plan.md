---
title: Online Kubernetes installation with containerd
type: feature
created: 2026-10-21
status: done
baseline_commit: f2380d3fff5839bc0a70e5686e76fe360a4fc403
context: []
---

<frozen-after-approval reason="User explicitly requested containerd, Kubernetes v1.37.1, online installation and Istio 1.31.2">

## Intent

Replace the Docker and offline-package installation path with online installation
using containerd. Set Kubernetes v1.37.1 and Istio 1.31.2 as the default versions,
with `config.yaml` values controlling the actual installation. Audit and replace
the supporting component installation scripts so no local archive, deb bundle or
Docker image load is required.

## Boundaries and Constraints

Always preserve prior SSH verification/bypass, cancellation, topology validation,
and safe HA synchronization. Embed versioned scripts in the executable so a fresh
checkout and standalone binary do not depend on the external resource symlink.
Download version-specific artifacts and verify published checksums where supplied.
Generate kubeadm configuration for the installed kubeadm API and systemd cgroups.
Fail explicitly on unsupported platforms, unavailable package versions, existing
Docker/other-runtime clusters or a cluster-version mismatch during expansion.

Never deploy to a real cluster, uninstall a running runtime, migrate existing
workloads automatically, change the user-requested versions, or modify/delete the
external resource symlink target. Existing-cluster multi-minor upgrades require a
separate rollout procedure; `--update` remains node expansion.

## Input and Edge Cases

| State | Behavior |
| --- | --- |
| Fresh Ubuntu/Debian node, amd64 or arm64 | Install pinned runtime and Kubernetes tools online |
| Existing Docker-backed cluster or mismatched Kubernetes version | Reject expansion before mutating nodes |
| Missing release or checksum mismatch | Fail without silently selecting latest or older artifacts |
| New worker | Join using containerd, pull workload images online |
| New control-plane | Serial join and safe HA peer refresh |
| No NTP/NFS configured | Skip optional services; retain required client utilities |

</frozen-after-approval>

## Code Map

- `app/resources/` — embedded online scripts, supporting component version pins and shell regression fixtures.
- `app/node/` — upload embedded scripts with bounded SFTP operations.
- `app/engine/` — containerd install, Kubernetes image pull, online addons, expansion preflight.
- `app/config/`, `config.yaml`, `main.go` — default socket, fixed release settings and dependency assembly.
- `README.MD`, `README.zh.MD` — online requirements, versions, steps and migration scope.

## Tasks and Acceptance

- [x] Add tracked online scripts for init, containerd/runc/CNI, kubeadm, image pull, Calico, Helm, HAProxy, Keepalived, Chrony, NFS and metrics addons; pin releases centrally.
- [x] Embed scripts and update Node staging without accessing the old resource symlink.
- [x] Replace Docker and image-load scheduling with online installation and pulling; use configured Kubernetes/Istio versions and the containerd CRI socket throughout init and join.
- [x] Validate existing control-plane Kubernetes and runtime versions before expansion; do not claim to migrate a running Docker cluster.
- [x] Update default configuration and bilingual documentation; explain changed image-pull step numbers.
- [x] Add meaningful script, staging, version propagation, expansion-preflight and no-offline-dependency regression tests.
- [x] Run shell syntax checks, Go tests/race/vet/build, CLI smoke checks and independent review.

Acceptance criteria:

- Given a standalone executable, when staging a component, then scripts come from embedded tracked files rather than a local resource directory.
- Given a new deployment, when installing its control-plane, then kubeadm/kubelet/kubectl and cluster configuration all use v1.37.1 and containerd with systemd cgroups.
- Given addon installation, when deploying Istio, then both its CLI and control/data-plane images use 1.31.2 without local image archives.
- Given a failed version lookup or download, when installing, then the script exits and does not fall back to another release.
- Given an incompatible existing cluster, when expanding, then validation fails before initialization or runtime changes on any node.

## Design Notes

Retain the existing kube-system Calico DaemonSet readiness contract by using its
versioned manifest. Online package installation supports apt-based Linux targets;
other platforms fail before system changes. All nodes require network access to
OS package mirrors and component repositories, and kubelets need registry access.

Upstream checks: Kubernetes v1.37.1 release exists; current Istio support matrix
includes Kubernetes 1.37 for Istio 1.31; Calico 3.33 lists Kubernetes 1.37.

## Spec Change Log

- Review found that legacy Istio recovery could hide download, checksum or addon
  failures behind existing Ready deployments. Any installer error now terminates
  the step, including timeout; readiness recovery only runs after script success.
- Review found Loki's PVC would remain Pending with optional NFS disabled. New
  installations select PVC only when a default StorageClass exists, otherwise
  use emptyDir. Existing StatefulSets retain their immutable storage layout.
- Minimal Debian root targets need sudo for existing remote command helpers;
  init installs it online before hosts and component operations.
- Follow-up clarified that `config.yaml` is the sole source for Kubernetes and
  Istio versions. Both fields are required per config file and flow through package installs,
  kubeadm initialization, image pulls, Istio install and expansion checks; input
  validation accepts stable release formats and rejects shell-like values.
- Fixed kubeadm package lookup returning exit 141: its first-match `awk` used to
  exit early and SIGPIPE `apt-cache` under `pipefail`. The lookup now consumes the
  full output while retaining the first exact package version.
- Image preparation now checks containerd's `k8s.io` namespace per reference and
  pulls only missing kubeadm images, supporting locally preloaded image archives.

## Verification

Verification completed:

- `go test -race ./... -timeout 60s` passed across all packages, including embedded
  shell script and local SFTP staging regression checks.
- `go vet ./...` and `go build -o /tmp/k8s-tool-online .` passed.
- CLI step listing passed for fresh install and update flows; help output exposes
  the SSH host-key bypass option with its secure default (`false`).
- `git diff --check` passed. Shell syntax and online-only assertions run in the
  `app/resources` test suite.
- Independent reviews covered online scripts, implementation acceptance,
  architecture and edge cases; identified issues were fixed and re-reviewed.

No real deployment was performed. An existing Docker-backed v1.29 cluster is not
automatically migrated; expansion rejects runtime or version mismatches. Use a
separately planned, staged cluster migration for existing workloads.

Final version-configuration regression tests, full Go tests, vet, build and
whitespace checks passed after making config.yaml the sole Kubernetes/Istio
version source.
