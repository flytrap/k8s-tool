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

const nodeJoinTimeout = 5 * time.Minute

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
	res, err := e.master.Run(filepath.Join("resource", "kubeadm"),
		fmt.Sprintf("bash start.sh %s %s %s", e.vip, e.master.GetHostname(), e.master.GetAddress()))
	if err != nil {
		return err
	}
	logrus.Info(res)

	if err := e.configureKubectl(e.master); err != nil {
		return err
	}
	if err := e.waitForNodeRegistered(e.master); err != nil {
		return err
	}
	if err := e.removeControlPlaneTaints(e.master.GetHostname()); err != nil {
		return err
	}

	return e.joinNodes()
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
		_, err := e.master.Run("", fmt.Sprintf("kubectl get node %s", shellQuote(hostname)))
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: node was not registered in Kubernetes within %s: %w", hostname, nodeJoinTimeout, lastErr)
		}
		time.Sleep(5 * time.Second)
	}
}

func (e *Engine) kubeadmJoinCommand(baseJoin string, controlPlane bool, certKey string) string {
	parts := []string{"sudo", baseJoin}
	if controlPlane {
		parts = append(parts, "--control-plane", "--certificate-key", certKey)
	}
	criSocket := strings.TrimSpace(e.CRISocket)
	if criSocket != "" {
		parts = append(parts, shellQuote("--cri-socket="+criSocket))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (e *Engine) joinNodes() error {
	certKeyBytes, err := e.master.Run("", "sudo kubeadm certs certificate-key")
	if err != nil {
		return err
	}
	certKey := strings.TrimSpace(string(certKeyBytes))

	if _, err := e.master.Run("", fmt.Sprintf("sudo kubeadm init phase upload-certs --upload-certs --certificate-key=%s", certKey)); err != nil {
		return err
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
		cmd := e.kubeadmJoinCommand(baseJoin, true, certKey)
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
			cmd := e.kubeadmJoinCommand(baseJoin, false, "")
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
	return err
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
	return e.master.Install("istio")
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

	return e.joinNodes()
}
