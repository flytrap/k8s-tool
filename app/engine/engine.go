package engine

import (
	"errors"
	"fmt"
	"k8s-tool/app/node"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	nodeJoinTimeout     = 5 * time.Minute
	istioInstallTimeout = 10 * time.Minute
)

type Engine struct {
	namespace string
	CRISocket string
	vip       string
	ntp       struct {
		server   string
		allow    string
		timezone string
	}
	nfs struct {
		server string
		path   string
	}
	registry struct {
		hostname string
	}
	master     node.Node
	nodes      []node.Node
	OnNextStep func(string)
}

func New(opts ...Option) (*Engine, error) {
	e := &Engine{namespace: ""}
	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (e *Engine) AddNode(n node.Node) error {
	addr := n.GetAddress()
	for i := range e.nodes {
		if addr == e.nodes[i].GetAddress() {
			return fmt.Errorf("%s: node already in cluster", addr)
		}
	}

	if n.IsControl() {
		if e.master == nil {
			e.master = n
		}
	}
	e.nodes = append(e.nodes, n)
	return nil
}

func (e *Engine) check() error {
	for _, n := range e.nodes {
		addr := n.GetAddress()
		if e.ntp.server == addr && e.ntp.allow == "" {
			return errors.New("ntp allow is empty")
		}
	}

	switch len(e.nodes) {
	case 0:
		return errors.New("cluster must have at least one node")
	case 1:
		n := e.nodes[0]
		if !n.IsETCD() || !n.IsControl() || !n.IsWorker() {
			return errors.New("single node need all roles (etcd controlplane worker)")
		}
	default:
		var t, c, w int
		for _, n := range e.nodes {
			if n.IsETCD() {
				t++
			}
			if n.IsControl() {
				c++
			}
			if n.IsWorker() {
				w++
			}
		}
		if t == 0 {
			return errors.New("cluster doesn't have etcd node")
		}
		if c == 0 {
			return errors.New("cluster doesn't have control plane node")
		}
		if w == 0 {
			return errors.New("cluster doesn't have worker node")
		}
	}
	return nil
}

func (e *Engine) Install(steps string) error {
	if err := e.check(); err != nil {
		return err
	}
	defer e.closeAll()
	if len(steps) > 0 {
		nums, err := parseStepNums(steps, len(DeploySteps))
		if err != nil {
			return err
		}
		nums = prependMissingSteps(nums, 1)
		for _, n := range nums {
			if err := DeploySteps[n-1].install(e); err != nil {
				return err
			}
		}
		return nil
	}

	for _, step := range DeploySteps {
		if err := step.install(e); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Update(steps string) error {
	if err := e.check(); err != nil {
		return err
	}
	defer e.closeAll()
	if len(steps) > 0 {
		nums, err := parseStepNums(steps, len(UpdateSteps))
		if err != nil {
			return err
		}
		required := []int{1}
		for _, n := range nums {
			if n > 2 {
				required = append(required, 2)
				break
			}
		}
		nums = prependMissingSteps(nums, required...)
		for _, n := range nums {
			if err := UpdateSteps[n-1].install(e); err != nil {
				return err
			}
		}
		return nil
	}
	for _, step := range UpdateSteps {
		if err := step.install(e); err != nil {
			return err
		}
	}
	return nil
}

func parseStepNums(raw string, max int) ([]int, error) {
	var nums []int
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		n, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", item, err)
		}
		if n < 1 || n > max {
			return nil, fmt.Errorf("invalid step %d: valid range is 1-%d", n, max)
		}
		nums = append(nums, n)
	}
	return nums, nil
}

func prependMissingSteps(nums []int, required ...int) []int {
	added := make(map[int]struct{}, len(nums)+len(required))
	out := make([]int, 0, len(nums)+len(required))
	for _, n := range required {
		if _, ok := added[n]; ok {
			continue
		}
		out = append(out, n)
		added[n] = struct{}{}
	}
	for _, n := range nums {
		if _, ok := added[n]; ok {
			continue
		}
		out = append(out, n)
		added[n] = struct{}{}
	}
	return out
}

func (e *Engine) closeAll() {
	for _, n := range e.nodes {
		if closer, ok := n.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

func (e *Engine) checkNew() error {
	res, err := e.master.Run("", "kubectl get nodes -o jsonpath='{.items[*].metadata.name}'")
	if err != nil {
		return err
	}
	nodeMap := map[string]struct{}{}
	for _, n := range strings.Fields(string(res)) {
		nodeMap[n] = struct{}{}
	}
	for i := range e.nodes {
		n := e.nodes[i]
		if _, ok := nodeMap[n.GetHostname()]; ok {
			n.SetIsNew(false)
		}
	}
	return nil
}

func (e *Engine) connect() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		eg.Go(func() error {
			return n.Connect()
		})
	}
	return eg.Wait()
}

func (e *Engine) init() error {
	hosts := make(map[string]string)
	for _, n := range e.nodes {
		addr := n.GetAddress()
		hosts[addr] = n.GetHostname()
	}

	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			password := n.GetPassword()
			hostname := n.GetHostname()
			if err := n.Install("init", password, hostname); err != nil {
				return err
			}

			for addr, name := range hosts {
				if err := n.AddHost(addr, name); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return eg.Wait()
}

func (e *Engine) installChrony() error {
	if e.ntp.server == "" {
		return nil
	}
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("chrony", e.ntp.server, e.ntp.allow, e.ntp.timezone)
		})
	}
	return eg.Wait()
}

func (e *Engine) installDocker() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("docker", e.registry.hostname)
		})
	}
	return eg.Wait()
}

func (e *Engine) loadDockerImage() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("docker/images")
		})
	}
	return eg.Wait()
}

func (e *Engine) installKubeadm() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("kubeadm")
		})
	}
	return eg.Wait()
}

func (e *Engine) installHa() error {
	nodes := []string{}
	for _, n := range e.nodes {
		if n.IsControl() {
			nodes = append(nodes, fmt.Sprintf("'server %s %s:6443 check'", n.GetHostname(), n.GetAddress()))
		}
	}
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsETCD() {
			continue
		}
		eg.Go(func() error {
			err := n.Install("haproxy", nodes...)
			if err != nil {
				return err
			}
			return nil
		})
	}
	return eg.Wait()
}

func (e *Engine) installKeepalived() error {
	var eg errgroup.Group
	var masterIps []string
	for _, n := range e.nodes {
		if n.IsControl() {
			masterIps = append(masterIps, n.GetAddress())
		}
	}
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsETCD() {
			continue
		}
		state := "BACKUP"
		if n == e.master {
			state = "MASTER"
		}
		var ips = []string{}
		for _, ip := range masterIps {
			if ip != n.GetAddress() {
				ips = append(ips, ip)
			}
		}
		eg.Go(func() error {
			if err := n.Install("keepalived", e.vip, state, n.GetAddress(), strings.Join(ips, ",")); err != nil {
				return err
			}
			if n != e.master {
				return n.StopService("keepalived")
			}
			return nil
		})
	}
	return eg.Wait()
}
func (e *Engine) startK8s() error {
	e.logCRISocket()
	args := []string{e.vip, e.master.GetHostname(), e.master.GetAddress()}
	if e.CRISocket != "" {
		args = append(args, e.CRISocket)
	}
	res, err := e.master.Run(filepath.Join("resource", "kubeadm"),
		fmt.Sprintf("bash start.sh %s", strings.Join(args, " ")))
	if err != nil {
		return err
	}
	logrus.Info(string(res))

	certKey := parseCertKey(string(res))

	if err := e.configureKubectl(e.master); err != nil {
		return err
	}
	if err := e.waitForNodeRegistered(e.master); err != nil {
		return err
	}
	if err := e.removeControlPlaneTaints(e.master.GetHostname()); err != nil {
		return err
	}

	return e.joinNodes(certKey)
}

func parseCertKey(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if strings.Contains(line, "certificate-key") || strings.Contains(line, "Using certificate key") {
			if i+1 < len(lines) {
				key := strings.TrimSpace(lines[i+1])
				if len(key) == 64 && isHex(key) {
					return key
				}
			}
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (e *Engine) uploadCerts() (string, error) {
	certKeyBytes, err := e.master.Run("", "sudo kubeadm certs certificate-key")
	if err != nil {
		return "", err
	}
	certKey := strings.TrimSpace(string(certKeyBytes))

	if _, err := e.master.Run(filepath.Join("resource", "kubeadm"), fmt.Sprintf(
		"sudo kubeadm init phase upload-certs --upload-certs --certificate-key=%s --config=kubeadm-config.yaml",
		certKey)); err != nil {
		return "", err
	}
	return certKey, nil
}

func (e *Engine) configureKubectl(n node.Node) error {
	if _, err := n.Run(filepath.Join("resource", "kubeadm"), "bash config.sh"); err != nil {
		return fmt.Errorf("%s: configure kubectl: %w", n.GetHostname(), err)
	}
	return nil
}

func (e *Engine) removeControlPlaneTaints(hostname string) error {
	for _, key := range []string{
		"node-role.kubernetes.io/master",
		"node-role.kubernetes.io/control-plane",
	} {
		cmd := fmt.Sprintf("kubectl taint node %s %s- 2>&1", shellQuote(hostname), shellQuote(key))
		out, err := e.master.Run("", cmd)
		if err == nil {
			continue
		}

		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "taint") && strings.Contains(msg, "not found") {
			continue
		}
		return fmt.Errorf("%s: remove taint %s: %w", hostname, key, err)
	}
	return nil
}

func (e *Engine) waitForNodeRegistered(n node.Node) error {
	hostname := n.GetHostname()
	deadline := time.Now().Add(nodeJoinTimeout)
	var lastErr error

	for {
		out, err := e.master.Run("", fmt.Sprintf("kubectl get node %s --ignore-not-found -o name", shellQuote(hostname)))
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if name, ok := e.nodeNameByInternalIP(n.GetAddress()); ok {
			return fmt.Errorf("%s: node registered as %q, expected %q; reset the node or join with --node-name",
				n.GetAddress(), name, hostname)
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("%s: node was not registered in Kubernetes within %s: %w", hostname, nodeJoinTimeout, lastErr)
			}
			return fmt.Errorf("%s: node was not registered in Kubernetes within %s", hostname, nodeJoinTimeout)
		}
		time.Sleep(5 * time.Second)
	}
}

func (e *Engine) nodeNameByInternalIP(addr string) (string, bool) {
	cmd := fmt.Sprintf(
		"kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{\"\\t\"}{range .status.addresses[?(@.type==\"InternalIP\")]}{.address}{\"\\n\"}{end}{end}' | awk -v ip=%s '$2 == ip {print $1; exit}'",
		shellQuote(addr))
	out, err := e.master.Run("", cmd)
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(out))
	return name, name != ""
}

func (e *Engine) kubeadmJoinCommand(n node.Node, baseJoin string, controlPlane bool, certKey string) string {
	parts := []string{"sudo", baseJoin}
	if controlPlane {
		parts = append(parts, "--control-plane", "--certificate-key", certKey)
	}
	if hostname := strings.TrimSpace(n.GetHostname()); hostname != "" {
		parts = append(parts, shellQuote("--node-name="+hostname))
	}
	parts = append(parts, e.criSocketArg())
	return strings.TrimRight(strings.Join(parts, " "), " ")
}

func (e *Engine) criSocketArg() string {
	criSocket := strings.TrimSpace(e.CRISocket)
	if criSocket == "" {
		return ""
	}
	return shellQuote("--cri-socket=" + criSocket)
}

func (e *Engine) logCRISocket() {
	criSocket := strings.TrimSpace(e.CRISocket)
	if criSocket == "" {
		logrus.Warn("cri-socket is empty; kubeadm may fail when multiple CRI endpoints exist on a node")
		return
	}
	logrus.Infof("Using CRI socket: %s", criSocket)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (e *Engine) joinNodes(certKey string) error {
	if certKey == "" {
		var err error
		certKey, err = e.uploadCerts()
		if err != nil {
			return err
		}
	}

	baseJoinBytes, err := e.master.Run("", "sudo kubeadm token create --print-join-command")
	if err != nil {
		return err
	}
	baseJoin := strings.TrimSpace(string(baseJoinBytes))

	// control-plane 逐个串行 join，etcd 成员变更不允许并发
	for i := range e.nodes {
		n := e.nodes[i]
		if n == e.master || !n.IsNew() || !n.IsControl() {
			continue
		}
		cmd := e.kubeadmJoinCommand(n, baseJoin, true, certKey)
		logrus.Infof("Joining control-plane node %s with command: %s", n.GetHostname(), maskJoinCommand(cmd))
		if _, err := n.Run("", cmd); err != nil {
			return err
		}
		if err := e.waitForNodeRegistered(n); err != nil {
			return err
		}
		if err := e.configureKubectl(n); err != nil {
			return err
		}
		if err := e.removeControlPlaneTaints(n.GetHostname()); err != nil {
			return err
		}
	}

	// worker 并行 join
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if n == e.master || !n.IsNew() || !n.IsWorker() || n.IsControl() {
			continue
		}
		eg.Go(func() error {
			cmd := e.kubeadmJoinCommand(n, baseJoin, false, "")
			logrus.Infof("Joining worker node %s with command: %s", n.GetHostname(), maskJoinCommand(cmd))
			_, err := n.Run("", cmd)
			return err
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	for i := range e.nodes {
		n := e.nodes[i]
		if n == e.master || !n.IsNew() || !n.IsWorker() || n.IsControl() {
			continue
		}
		if err := e.waitForNodeRegistered(n); err != nil {
			return err
		}
	}
	return nil
}

func maskJoinCommand(cmd string) string {
	fields := strings.Fields(cmd)
	for i := range fields {
		if fields[i] == "--token" && i+1 < len(fields) {
			fields[i+1] = "****"
		}
		if strings.HasPrefix(fields[i], "--token=") {
			fields[i] = "--token=****"
		}
		if fields[i] == "--certificate-key" && i+1 < len(fields) {
			fields[i+1] = "****"
		}
		if strings.HasPrefix(fields[i], "--certificate-key=") {
			fields[i] = "--certificate-key=****"
		}
	}
	return strings.Join(fields, " ")
}

func (e *Engine) installCalico() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("calico")
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	_, err := e.master.Run(filepath.Join("resource", "calico"), "kubectl apply -f calico.yaml")
	if err != nil {
		return err
	}
	return e.waitForClusterNetworkReady()
}

func (e *Engine) installHelm() error {
	return e.master.Install("helm")
}

func (e *Engine) installNFSUtils() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("nfs/nfs-utils")
		})
	}
	return eg.Wait()
}

func (e *Engine) installNFS() error {
	if err := e.installNFSUtils(); err != nil {
		return err
	}

	if e.nfs.server == "" {
		return nil
	}
	return e.master.Install("nfs", e.nfs.server, e.nfs.path, e.namespace)
}

func (e *Engine) installIstio() error {
	if err := e.waitForClusterNetworkReady(); err != nil {
		return err
	}

	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("istio/images")
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	installErr := e.master.InstallWithTimeout("istio", istioInstallTimeout)
	if installErr != nil {
		logrus.Warnf("istio install did not complete cleanly: %v", installErr)
	}
	if err := e.ensureIstioReady(installErr != nil); err != nil {
		if installErr != nil {
			return fmt.Errorf("istio install failed: %v; recovery check failed: %w", installErr, err)
		}
		return err
	}
	return nil
}

func (e *Engine) waitForClusterNetworkReady() error {
	checks := []struct {
		name string
		cmd  string
	}{
		{
			name: "calico-node daemonset",
			cmd:  "kubectl -n kube-system rollout status daemonset/calico-node --timeout=5m",
		},
		{
			name: "all nodes Ready",
			cmd:  "kubectl wait --for=condition=Ready nodes --all --timeout=5m",
		},
		{
			name: "CoreDNS deployment",
			cmd:  "kubectl -n kube-system rollout status deployment/coredns --timeout=5m",
		},
	}
	for _, check := range checks {
		logrus.Infof("Waiting for %s", check.name)
		out, err := e.master.Run("", check.cmd)
		if len(out) > 0 {
			logrus.Info(string(out))
		}
		if err != nil {
			return fmt.Errorf("%s is not ready: %w", check.name, err)
		}
	}
	return nil
}

func (e *Engine) ensureIstioReady(restartFirst bool) error {
	if restartFirst {
		if err := e.restartIstiod(); err != nil {
			return err
		}
	}
	if err := e.waitForIstiodReady(); err != nil {
		logrus.Warnf("istiod is not ready, restarting it: %v", err)
		if restartErr := e.restartIstiod(); restartErr != nil {
			return restartErr
		}
		if err := e.waitForIstiodReady(); err != nil {
			return err
		}
	}
	if err := e.waitForIstioGatewayReady(); err != nil {
		logrus.Warnf("istio ingress gateway is not ready, restarting istiod: %v", err)
		if restartErr := e.restartIstiod(); restartErr != nil {
			return restartErr
		}
		if err := e.waitForIstiodReady(); err != nil {
			return err
		}
		return e.waitForIstioGatewayReady()
	}
	return nil
}

func (e *Engine) restartIstiod() error {
	logrus.Warn("Restarting istiod deployment")
	out, err := e.master.Run("", "kubectl -n istio-system rollout restart deployment/istiod")
	if len(out) > 0 {
		logrus.Info(string(out))
	}
	if err != nil {
		return fmt.Errorf("restart istiod: %w", err)
	}
	return nil
}

func (e *Engine) waitForIstiodReady() error {
	if err := e.runAndLog("istiod rollout", "kubectl -n istio-system rollout status deployment/istiod --timeout=5m"); err != nil {
		return err
	}
	return e.waitForIstiodEndpoints()
}

func (e *Engine) waitForIstiodEndpoints() error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := e.master.Run("", "kubectl -n istio-system get endpoints istiod -o jsonpath='{.subsets[*].addresses[*].ip}'")
		if err == nil && strings.TrimSpace(string(out)) != "" {
			logrus.Infof("istiod endpoints: %s", strings.TrimSpace(string(out)))
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("wait for istiod endpoints: %w", err)
			}
			return errors.New("wait for istiod endpoints: no ready endpoint")
		}
		time.Sleep(5 * time.Second)
	}
}

func (e *Engine) waitForIstioGatewayReady() error {
	return e.runAndLog("istio ingressgateway rollout", "kubectl -n istio-system rollout status deployment/istio-ingressgateway --timeout=5m")
}

func (e *Engine) runAndLog(name, cmd string) error {
	logrus.Infof("Waiting for %s", name)
	out, err := e.master.Run("", cmd)
	if len(out) > 0 {
		logrus.Info(string(out))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (e *Engine) installApp() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("app/images")
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	if err := e.master.Install("app"); err != nil {
		return err
	}
	return e.startKeepalivedBackups()
}

func (e *Engine) startKeepalivedBackups() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if n == e.master || !n.IsETCD() {
			continue
		}
		eg.Go(func() error {
			return n.StartService("keepalived")
		})
	}
	return eg.Wait()
}

func (e *Engine) join() error {
	// 新节点先并行加载镜像
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			if err := n.Install("docker/images"); err != nil {
				return err
			}
			if err := n.Install("istio/images"); err != nil {
				return err
			}
			return n.Install("app/images")
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	return e.joinNodes("")
}
