package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-tool/app/node"
	"k8s-tool/app/resources"
)

const (
	testKubernetesVersion = "v1.37.1"
	testIstioVersion      = "1.31.2"
)

type fakeNode struct {
	node.Node
	mu                                            sync.Mutex
	events                                        []string
	run                                           func(context.Context, string) ([]byte, error)
	installErr                                    error
	connectErr                                    error
	haActive                                      *bool
	onInstall                                     func(string)
	kubeletVersion, runtimeVersion, serverVersion string
}

func newFake(t *testing.T, name, addr string, roles ...string) *fakeNode {
	t.Helper()
	n, err := node.New(node.Address(addr), node.Role(roles))
	if err != nil {
		t.Fatal(err)
	}
	n.SetHostname(name)
	return &fakeNode{Node: n}
}
func (n *fakeNode) record(event string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
}
func (n *fakeNode) has(part string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, v := range n.events {
		if strings.Contains(v, part) {
			return true
		}
	}
	return false
}
func (n *fakeNode) Connect() error { return n.ConnectContext(context.Background()) }
func (n *fakeNode) ConnectContext(ctx context.Context) error {
	n.record("connect")
	if err := ctx.Err(); err != nil {
		return err
	}
	return n.connectErr
}
func (n *fakeNode) Close() { n.record("close") }
func (n *fakeNode) Run(cwd string, cmds ...string) ([]byte, error) {
	return n.RunContext(context.Background(), cwd, cmds...)
}
func (n *fakeNode) RunContext(ctx context.Context, cwd string, cmds ...string) ([]byte, error) {
	cmd := strings.Join(cmds, " && ")
	n.record("run:" + cwd + ":" + cmd)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cmd == keepalivedStatusCommand {
		active := !n.IsNew() || n.has("install:keepalived:")
		if n.haActive != nil {
			active = *n.haActive
		}
		if active {
			return []byte("active"), nil
		}
		return []byte("inactive"), nil
	}
	if strings.Contains(cmd, "kubectl version -o json") {
		version := n.serverVersion
		if version == "" {
			version = testKubernetesVersion
		}
		return []byte(fmt.Sprintf(`{"serverVersion":{"gitVersion":%q}}`, version)), nil
	}
	if n.run != nil {
		return n.run(ctx, cmd)
	}
	return nil, nil
}
func (n *fakeNode) Install(name string, args ...string) error {
	n.record("install:" + name + ":" + strings.Join(args, "|"))
	if n.onInstall != nil {
		n.onInstall(name)
	}
	return n.installErr
}
func (n *fakeNode) InstallWithTimeout(name string, _ time.Duration, args ...string) error {
	return n.Install(name, args...)
}
func (n *fakeNode) Stage(name string) error { n.record("stage:" + name); return nil }
func (n *fakeNode) AddHost(addr, name string) error {
	n.record("host:" + addr + ":" + name)
	return nil
}
func (n *fakeNode) StopService(name string) error  { n.record("stop:" + name); return nil }
func (n *fakeNode) StartService(name string) error { n.record("start:" + name); return nil }
func engineFor(t *testing.T, nodes ...*fakeNode) *Engine {
	t.Helper()
	e, err := New(Vip("192.0.2.100"), KubernetesVersion(testKubernetesVersion), IstioVersion(testIstioVersion))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := e.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	return e
}
func inventory(nodes ...*fakeNode) []byte {
	var items []any
	for _, n := range nodes {
		labels := map[string]string{}
		if n.IsControl() {
			labels["node-role.kubernetes.io/control-plane"] = ""
		}
		kubeletVersion, runtimeVersion := n.kubeletVersion, n.runtimeVersion
		if kubeletVersion == "" {
			kubeletVersion = testKubernetesVersion
		}
		if runtimeVersion == "" {
			runtimeVersion = "containerd://" + resources.ContainerdVersion
		}
		items = append(items, map[string]any{"metadata": map[string]any{"name": n.GetHostname(), "labels": labels}, "status": map[string]any{"nodeInfo": map[string]string{"kubeletVersion": kubeletVersion, "containerRuntimeVersion": runtimeVersion}, "addresses": []any{map[string]string{"type": "InternalIP", "address": n.GetAddress()}}}})
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return b
}
func TestUnsupportedTopologyRejectedBeforeConnection(t *testing.T) {
	for _, roles := range [][]string{{"etcd"}, {"controlplane"}, {}} {
		t.Run(fmt.Sprint(roles), func(t *testing.T) {
			bad := newFake(t, "bad", "192.0.2.1", roles...)
			control := newFake(t, "master", "192.0.2.2", "etcd", "controlplane", "worker")
			e := engineFor(t, bad, control)
			if err := e.Install(""); err == nil {
				t.Fatal("unsupported topology accepted")
			}
			if bad.has("connect") || control.has("connect") {
				t.Fatal("connected before validation")
			}
		})
	}
}
func TestDuplicateHostnamesRejected(t *testing.T) {
	first := newFake(t, "same", "192.0.2.1", "etcd", "controlplane", "worker")
	e := engineFor(t, first)
	if err := e.AddNode(newFake(t, "same", "192.0.2.2", "worker")); err == nil {
		t.Fatal("duplicate hostname accepted")
	}
}
func TestDiscoveryIgnoresConfigurationOrder(t *testing.T) {
	fresh := newFake(t, "new-master", "192.0.2.2", "etcd", "controlplane", "worker")
	old := newFake(t, "old-master", "192.0.2.1", "etcd", "controlplane", "worker")
	fresh.run = func(context.Context, string) ([]byte, error) { return nil, errors.New("kubectl not installed") }
	old.run = func(context.Context, string) ([]byte, error) { return inventory(old), nil }
	e := engineFor(t, fresh, old)
	if err := e.Update("2"); err != nil {
		t.Fatal(err)
	}
	if e.master != old || !fresh.IsNew() || old.IsNew() {
		t.Fatal("incorrect management node or membership")
	}
	if fresh.has("install:") || old.has("install:") {
		t.Fatal("discovery mutated cluster")
	}
}
func TestDiscoveryRejectsIncompleteInventoryAndIdentity(t *testing.T) {
	for _, scenario := range []string{"missing-control", "wrong-address", "renamed-address", "role-change", "no-existing-control"} {
		t.Run(scenario, func(t *testing.T) {
			old := newFake(t, "old-master", "192.0.2.1", "etcd", "controlplane", "worker")
			fresh := newFake(t, "new-master", "192.0.2.2", "etcd", "controlplane", "worker")
			remote := newFake(t, "other-master", "192.0.2.3", "etcd", "controlplane", "worker")
			var data []byte
			switch scenario {
			case "missing-control":
				data = inventory(old, remote)
			case "wrong-address":
				data = inventory(old, newFake(t, "new-master", "192.0.2.99", "etcd", "controlplane", "worker"))
			case "renamed-address":
				data = inventory(old, newFake(t, "other-name", "192.0.2.2", "worker"))
			case "role-change":
				data = inventory(old, newFake(t, "new-master", "192.0.2.2", "worker"))
			case "no-existing-control":
				data = []byte(`{"items":[]}`)
			}
			old.run = func(context.Context, string) ([]byte, error) { return data, nil }
			fresh.run = func(context.Context, string) ([]byte, error) { return nil, errors.New("unavailable") }
			e := engineFor(t, fresh, old)
			if err := e.Update("3"); err == nil {
				t.Fatal("invalid inventory accepted")
			}
			if fresh.has("install:") {
				t.Fatal("initialized before validating inventory")
			}
		})
	}
}
func TestHASynchronizationPreservesLiveBackupsAndCanRetry(t *testing.T) {
	master := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	backup := newFake(t, "backup", "192.0.2.2", "etcd", "controlplane", "worker")
	fresh := newFake(t, "fresh", "192.0.2.3", "etcd", "controlplane", "worker")
	worker := newFake(t, "worker", "192.0.2.4", "worker")
	master.SetIsNew(false)
	backup.SetIsNew(false)
	e := engineFor(t, master, backup, fresh, worker)
	for attempt := 0; attempt < 2; attempt++ {
		if err := e.syncHA(); err != nil {
			t.Fatal(err)
		}
		fresh.SetIsNew(false)
	}
	for _, n := range []*fakeNode{master, backup, fresh} {
		for _, name := range []string{"master", "backup", "fresh"} {
			if !n.has("server " + name + " ") {
				t.Fatalf("%s missing backend %s", n.GetHostname(), name)
			}
		}
		if !n.has("install:keepalived:") {
			t.Fatalf("%s missing keepalived", n.GetHostname())
		}
		if n.has("stop:") {
			t.Fatal("stopped a live HA service")
		}
	}
	if worker.has("install:") {
		t.Fatal("HA installed on worker")
	}
	order := e.haNodes()
	if order[len(order)-1] != master {
		t.Fatal("management node should be refreshed last")
	}
}

func TestHASynchronizationUpdatesLivePeersBeforeStartingJoinedNode(t *testing.T) {
	master := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	joined := newFake(t, "joined", "192.0.2.2", "etcd", "controlplane", "worker")
	backup := newFake(t, "backup", "192.0.2.3", "etcd", "controlplane", "worker")
	// Simulate a new process after a completed join: discovery marks all nodes
	// existing, but the joined node does not yet have a running HA service.
	for _, n := range []*fakeNode{master, joined, backup} {
		n.SetIsNew(false)
	}
	inactive := false
	joined.haActive = &inactive
	e := engineFor(t, joined, master, backup)
	e.master = master
	var refreshed []string
	for _, n := range []*fakeNode{master, joined, backup} {
		n := n
		n.onInstall = func(component string) {
			if component == "keepalived" {
				refreshed = append(refreshed, n.GetHostname())
			}
		}
	}
	if err := e.syncHA(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(refreshed) != "[backup master joined]" {
		t.Fatalf("unsafe peer refresh order: %v", refreshed)
	}
}

func TestHASynchronizationRefusesToStartWithoutLiveVIP(t *testing.T) {
	n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	e := engineFor(t, n)
	if err := e.syncHA(); err == nil {
		t.Fatal("expected missing live HA error")
	}
	if n.has("install:") {
		t.Fatal("changed HA before finding a live peer")
	}
}
func TestFullExpansionJoinsBeforeHASynchronization(t *testing.T) {
	old := newFake(t, "old", "192.0.2.1", "etcd", "controlplane", "worker")
	fresh := newFake(t, "fresh", "192.0.2.2", "etcd", "controlplane", "worker")
	old.run = func(_ context.Context, cmd string) ([]byte, error) {
		switch {
		case strings.Contains(cmd, "get nodes -o json --request-timeout"):
			return inventory(old), nil
		case strings.Contains(cmd, "certs certificate-key"):
			return []byte(strings.Repeat("a", 64)), nil
		case strings.Contains(cmd, "--print-join-command"):
			return []byte("kubeadm join 192.0.2.100:16443 --token test.token"), nil
		case strings.Contains(cmd, "get node 'fresh'"):
			return []byte("node/fresh"), nil
		default:
			return nil, nil
		}
	}
	fresh.run = func(_ context.Context, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "get nodes -o json") {
			return nil, errors.New("not initialized")
		}
		return nil, nil
	}
	e := engineFor(t, fresh, old)
	var steps []string
	e.OnNextStep = func(name string) { steps = append(steps, name) }
	if err := e.Update(""); err != nil {
		t.Fatal(err)
	}
	if !fresh.has("sudo kubeadm join") || old.has("sudo kubeadm join") {
		t.Fatal("wrong join target")
	}
	if !fresh.has("install:containerd:"+resources.ContainerdVersion) || !fresh.has("install:kubeadm:"+testKubernetesVersion) || !fresh.has("install:kubeadm/images:"+testKubernetesVersion+"|"+resources.DefaultCRISocket) {
		t.Fatal("online runtime/tool installation or image pull missing")
	}
	if fresh.has("docker") || fresh.has("/images:") && !fresh.has("install:kubeadm/images:") {
		t.Fatal("offline component installation retained")
	}
	if fresh.has("kubectl apply -f calico.yaml") {
		t.Fatal("applied cluster manifest on new node")
	}
	if !fresh.has("install:haproxy:") || !old.has("install:haproxy:") {
		t.Fatal("HA was not synchronized")
	}
	if old.has("stop:") || fresh.has("stop:") {
		t.Fatal("expansion stopped HA backup")
	}
	if steps[len(steps)-1] != "synchronize HA" {
		t.Fatalf("unexpected plan %v", steps)
	}
	if !old.has("close") || !fresh.has("close") {
		t.Fatal("connections not released")
	}
}
func TestPollingHonorsCancellation(t *testing.T) {
	n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	e := engineFor(t, n)
	ctx, cancel := context.WithCancel(context.Background())
	e.ctx = ctx
	n.run = func(ctx context.Context, _ string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("poll command missing deadline")
		}
		cancel()
		return nil, context.Canceled
	}
	started := time.Now()
	if err := e.waitForNodeRegistered(n); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation waited for retry interval")
	}
}
func TestParseStepNumsRejectsEmptyAndPreservesOrder(t *testing.T) {
	for _, raw := range []string{", ,", "0", "11", "x"} {
		if _, err := parseStepNums(raw, 10); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	nums, err := parseStepNums("9, 10,9", 10)
	if err != nil {
		t.Fatal(err)
	}
	nums = prependMissingSteps(nums, 1, 2)
	if fmt.Sprint(nums) != "[1 2 9 10]" {
		t.Fatal(nums)
	}
}

func TestExpansionRejectsLegacyVersionsBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"old-server", "old-kubelet", "docker", "old-containerd", "unknown-runtime"} {
		t.Run(scenario, func(t *testing.T) {
			old := newFake(t, "old", "192.0.2.1", "etcd", "controlplane", "worker")
			fresh := newFake(t, "fresh", "192.0.2.2", "worker")
			switch scenario {
			case "old-server":
				old.serverVersion = "v1.29.2"
			case "old-kubelet":
				old.kubeletVersion = "v1.29.2"
			case "docker":
				old.runtimeVersion = "docker://24.0.7"
			case "old-containerd":
				old.runtimeVersion = "containerd://1.6.36"
			case "unknown-runtime":
				old.runtimeVersion = "unknown"
			}
			old.run = func(context.Context, string) ([]byte, error) { return inventory(old), nil }
			e := engineFor(t, fresh, old)
			if err := e.Update(""); err == nil {
				t.Fatal("incompatible cluster accepted")
			}
			if old.has("install:") || fresh.has("install:") {
				t.Fatal("mutated nodes before compatibility validation")
			}
		})
	}
}

func TestOnlineComponentVersionsAndOrder(t *testing.T) {
	n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	n.run = func(_ context.Context, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "get endpoints istiod") {
			return []byte("10.244.0.10"), nil
		}
		return nil, nil
	}
	e := engineFor(t, n)
	if err := e.Install("4,5,6,7,11,13,14"); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"install:containerd:" + resources.ContainerdVersion + "|" + resources.RuncVersion + "|" + resources.CNIVersion,
		"install:kubeadm:" + testKubernetesVersion,
		"install:kubeadm/images:" + testKubernetesVersion + "|" + resources.DefaultCRISocket + "|registry.k8s.io",
		"install:helm:" + resources.HelmVersion,
		"install:calico:" + resources.CalicoVersion,
		"install:istio:" + testIstioVersion,
		"install:app:" + resources.MetricsServerVersion + "|" + resources.KubeStateMetricsChartVersion,
	} {
		if !n.has(part) {
			t.Fatalf("missing pinned component call %q", part)
		}
	}
	tool, pull := -1, -1
	for i, event := range n.events {
		if strings.HasPrefix(event, "install:kubeadm:") {
			tool = i
		}
		if strings.HasPrefix(event, "install:kubeadm/images:") {
			pull = i
		}
		if strings.Contains(event, "docker") || strings.Contains(event, "images:istio") || strings.Contains(event, "images:app") {
			t.Fatalf("legacy offline call: %s", event)
		}
	}
	if tool < 0 || pull <= tool {
		t.Fatal("image pull ran before kubeadm installation")
	}
}

func TestConfiguredVersionsReachInstallersAndInit(t *testing.T) {
	const kubeVersion = "v1.36.4"
	const istioVersion = "1.30.3"
	n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	n.run = func(_ context.Context, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "kubectl get node 'master' --ignore-not-found") {
			return []byte("node/master"), nil
		}
		if strings.Contains(cmd, "get endpoints istiod") {
			return []byte("10.244.0.10"), nil
		}
		return nil, nil
	}
	e, err := New(Vip("192.0.2.100"), KubernetesVersion(kubeVersion), IstioVersion(istioVersion))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddNode(n); err != nil {
		t.Fatal(err)
	}
	if err := e.installKubeadm(); err != nil {
		t.Fatal(err)
	}
	if err := e.pullKubernetesImages(); err != nil {
		t.Fatal(err)
	}
	if err := e.startK8s(); err != nil {
		t.Fatal(err)
	}
	if err := e.installIstio(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"install:kubeadm:" + kubeVersion,
		"install:kubeadm/images:" + kubeVersion,
		"bash start.sh '192.0.2.100' 'master' '192.0.2.1' '" + resources.DefaultCRISocket + "' '" + kubeVersion + "' 'registry.k8s.io'",
		"install:istio:" + istioVersion,
	} {
		if !n.has(want) {
			t.Fatalf("configured version did not reach installer: %q", want)
		}
	}
}

func TestInitPassesContainerdSocketAndPinnedVersion(t *testing.T) {
	n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
	n.run = func(_ context.Context, cmd string) ([]byte, error) {
		if strings.Contains(cmd, "kubectl get node") {
			return []byte("node/master"), nil
		}
		return nil, nil
	}
	e := engineFor(t, n)
	if err := e.startK8s(); err != nil {
		t.Fatal(err)
	}
	if !n.has("bash start.sh '192.0.2.100' 'master' '192.0.2.1' '" + resources.DefaultCRISocket + "' '" + testKubernetesVersion + "' 'registry.k8s.io'") {
		t.Fatal("init parameters missing pinned version or socket")
	}
	if n.has("certs certificate-key") || n.has("--print-join-command") {
		t.Fatal("single-node install created unused join credentials")
	}
}

func TestIstioInstallationErrorCannotBeHiddenByReadyDeployments(t *testing.T) {
	for _, failure := range []error{errors.New("download/checksum failed"), errors.New("addon apply failed"), context.DeadlineExceeded} {
		n := newFake(t, "master", "192.0.2.1", "etcd", "controlplane", "worker")
		n.installErr = failure
		n.run = func(context.Context, string) ([]byte, error) { return []byte("old deployment is Ready"), nil }
		e := engineFor(t, n)
		if err := e.installIstio(); !errors.Is(err, failure) {
			t.Fatalf("installation failure hidden: %v", err)
		}
		if n.has("rollout status deployment/istiod") || n.has("rollout restart") {
			t.Fatal("attempted to replace installer failure with readiness recovery")
		}
	}
}
