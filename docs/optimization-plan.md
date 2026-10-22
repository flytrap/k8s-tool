---
title: Reliable cluster installation and expansion
type: bugfix
created: 2026-10-21
status: done
baseline_commit: f2380d3
context: []
---

<frozen-after-approval reason="User authorized execution of the architecture review improvements">

## Intent

Make cluster installation and expansion safe, bounded, and independent of configuration ordering. Address all seven findings from the architecture review, with regression tests and documented configuration migration.

## Boundaries and Constraints

Always preserve existing install step numbers and update steps 1 through 9. Reject unsupported topology before connecting. Validate SSH host identities by default. Carry CLI cancellation through remote operations. Keep control-plane joins serial and worker joins parallel.

Never execute a deployment, modify the external `resource` symlink target, publish, push, or change existing user credentials. External script packaging and full independent-etcd support are separate follow-up work.

## Input and Edge Cases

| State | Expected behavior |
| --- | --- |
| Separate etcd and control-plane roles | Reject before remote mutation; explain supported colocated topology |
| New control-plane first in configuration | Discover a registered existing control-plane and use it for management |
| Additional control-plane joins | Synchronize HAProxy backends and Keepalived peers without stopping existing backups |
| Unknown or changed SSH host key | Reject; allow explicit SHA256 pin or trusted known_hosts |
| Shell metacharacters in hostname | Reject; commands also quote complete arguments correctly |
| Hanging SSH handshake, command, or transfer | Return bounded timeout; cancellation releases resources |
| Update step listing | Show actual update numbering, including appended HA synchronization |

</frozen-after-approval>

## Code Map

- `app/node/` handles SSH authentication, remote commands, transfers, and host entries.
- `app/engine/` validates topology and schedules installation, discovery, joining, and HA.
- `app/config/` parses YAML; `main.go` composes configuration and execution.
- `README.MD` and `README.zh.MD` describe usage and migration requirements.

## Tasks and Acceptance

Execution order is security and operation lifecycle, topology and expansion, CLI/configuration, regression checks, then review.

- [x] `app/node/` — correct hosts command quoting and exact hostname matching; quote working directories and service arguments; test hostile inputs without remote mutation.
- [x] `app/node/` — add known_hosts/SHA256 pin verification and configurable connection/operation timeouts; propagate context cancellation through handshake, session creation, commands and SFTP; test using local SSH fixtures.
- [x] `app/engine/` — validate hostname uniqueness and colocated etcd/control-plane roles; discover an existing registered management node regardless of list order; fail on inconsistent node identity or incomplete control-plane inventory.
- [x] `app/engine/` — append update HA synchronization, preserve live backup services, load offline Calico images on new nodes, and check network readiness; retain control-plane serialization.
- [x] `main.go`, `app/config/` — isolate configuration loading, provide effective defaults, propagate signal context and SSH settings, return actionable errors, and print mode-specific steps.
- [x] `README.MD`, `README.zh.MD` — document supported topology, trusted host setup, timeout settings, node inventory and update step behavior.
- [x] Regression tests — cover review triggers with fake nodes, shell fixtures, local SSH servers, config files, and CLI step listing.
- [x] Verification — run formatting, all tests, race detection, vet, build, and independent review; record results below.

Acceptance criteria:

- Given invalid topology or duplicate node identity, when installation starts, then it returns an actionable error before connecting.
- Given a new control-plane preceding registered nodes, when expansion runs, then discovery chooses an existing control-plane and the new node is joined rather than skipped.
- Given successful control-plane expansion, when HA synchronization finishes, then every configured control-plane receives the complete backend/peer inventory and existing backups have not been deliberately stopped.
- Given untrusted SSH identity or a stalled remote operation, when connecting/executing, then the operation fails securely within its deadline and releases local resources.
- Given update mode, when steps are printed, then the displayed numbers correspond to the update execution plan without loading cluster configuration.

## Design Notes

Prefer small focused topology, discovery, HA and SSH helpers over a broad framework rewrite. Existing resource scripts remain an integration contract: tests verify orchestration and remote command behavior; actual VIP failover and package installation require an isolated deployment environment.

## Spec Change Log

- User requested an explicit SSH verification bypass after the initial hardening.
  Added `ssh.insecure-skip-host-key-check` and the matching CLI flag, defaulting
  to false. Explicit CLI values override configuration, including false to
  restore verification. When enabled, both known_hosts and fingerprint checks
  are skipped. Regression tests cover defaults, precedence and mismatching keys.

- Independent edge-case review identified that starting a new Keepalived participant before existing peers were updated could create a second VRRP master. HA synchronization now probes actual service status, updates all running peers first, then starts inactive participants. This also covers retrying after a completed join in a new process. If no HA instance is active, synchronization fails before modifying HA. Preserve serial HAProxy refresh, complete peer inventory and the existing update step numbers.

## Verification

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build -o /tmp/k8s-tool-optimized .`

All commands passed. `git diff --check` and formatting checks passed. CLI smoke
checks confirmed the default `config.yaml` path and update steps 1–10 without
loading a cluster configuration. Regression tests cover hostile host input,
trusted/unknown/changed SSH host keys, handshake/channel/command/SFTP stalls,
cancellation and reconnect, nested component paths, invalid topology, membership
identity, complete control-plane inventory, configuration order, expansion,
live HA preservation and retry ordering.

Three independent review passes covered diff correctness, edge cases and
acceptance criteria. The HA ordering finding was fixed, tested and re-reviewed.

No real deployment was performed. External scripts were read for integration
context but not modified. Package installation, remote process behavior after
disconnect, network readiness and VIP failover require an isolated cluster.

## Follow-up Work

- Version the external resource package with a compatibility manifest and checksums.
- Validate resource scripts against configured CRI sockets, OS and architecture.
- Add isolated multi-node deployment and VIP failover exercises.
- Implement independent etcd topology only with a dedicated deployment design.

## Suggested Review Order

**Entry point and execution boundaries**

- Follow configuration, signal cancellation and node construction from the CLI.
  [main.go:68](../main.go#L68)
- Check supported topology before any remote connection.
  [topology.go:30](../app/engine/topology.go#L30)

**Expansion and high availability**

- Discover registered management nodes and verify complete control-plane inventory.
  [discovery.go:40](../app/engine/discovery.go#L40)
- Refresh live VRRP peers before starting additional participants, including retries.
  [ha.go:119](../app/engine/ha.go#L119)
- Inspect preserved join serialization and bounded registration polling.
  [cluster.go](../app/engine/cluster.go)

**Remote execution and regression coverage**

- Verify host identities and bound SSH handshake lifetime.
  [node.go:94](../app/node/node.go#L94)
- Close timed-out transports and join workers before returning shared buffers.
  [node.go:205](../app/node/node.go#L205)
- Review local SSH and hosts fixtures without production mutation.
  [node_test.go](../app/node/node_test.go)
- Inspect expansion, identity, HA ordering and cancellation regression scenarios.
  [engine_test.go](../app/engine/engine_test.go)
