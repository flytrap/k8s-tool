package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"k8s-tool/app/node"
)

func (e *Engine) startK8s() error {
	e.logCRISocket()
	args := []string{e.vip, e.master.GetHostname(), e.master.GetAddress(), e.CRISocket, e.KubernetesVersion, e.registry.hostname}
	res, err := e.master.Run(path.Join("resource", "kubeadm"),
		fmt.Sprintf("bash start.sh %s", quoteArgs(args)))
	if err != nil {
		return err
	}
	// kubeadm output contains bootstrap tokens and certificate keys.
	logrus.Info("Kubernetes control-plane initialized")

	certKey := parseCertKey(string(res))

	if err := e.configureKubectl(e.master); err != nil {
		return err
	}
	if err := e.waitForNodeRegistered(e.master); err != nil {
		return err
	}
	if e.master.IsWorker() {
		if err := e.removeControlPlaneTaints(e.master.GetHostname()); err != nil {
			return err
		}
	}

	if err := e.joinNodes(certKey); err != nil {
		return err
	}
	return e.startKeepalivedBackups()
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

	if _, err := e.master.Run("", fmt.Sprintf(
		"sudo kubeadm init phase upload-certs --upload-certs --skip-certificate-key-print --kubeconfig=/etc/kubernetes/admin.conf --certificate-key=%s",
		shellQuote(certKey))); err != nil {
		return "", err
	}
	return certKey, nil
}

func (e *Engine) configureKubectl(n node.Node) error {
	if err := n.Stage("kubeadm"); err != nil {
		return err
	}
	if _, err := n.Run(path.Join("resource", "kubeadm"), "bash config.sh"); err != nil {
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
	ctx, cancel := context.WithTimeout(e.ctx, nodeJoinTimeout)
	defer cancel()
	var lastErr error

	for {
		out, err := e.master.RunContext(ctx, "", fmt.Sprintf("kubectl get node %s --ignore-not-found -o name", shellQuote(hostname)))
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if name, ok := e.nodeNameByInternalIP(ctx, n.GetAddress()); ok {
			return fmt.Errorf("%s: node registered as %q, expected %q; reset the node or join with --node-name",
				n.GetAddress(), name, hostname)
		}
		if ctx.Err() != nil {
			if lastErr != nil {
				return fmt.Errorf("%s: node was not registered in Kubernetes within %s: %w", hostname, nodeJoinTimeout, errors.Join(ctx.Err(), lastErr))
			}
			return fmt.Errorf("%s: node registration: %w", hostname, ctx.Err())
		}
		if err := waitRetry(ctx); err != nil {
			return fmt.Errorf("%s: node registration: %w", hostname, err)
		}
	}
}

func (e *Engine) nodeNameByInternalIP(ctx context.Context, addr string) (string, bool) {
	cmd := fmt.Sprintf(
		"kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{\"\\t\"}{range .status.addresses[?(@.type==\"InternalIP\")]}{.address}{\"\\n\"}{end}{end}' | awk -v ip=%s '$2 == ip {print $1; exit}'",
		shellQuote(addr))
	out, err := e.master.RunContext(ctx, "", cmd)
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(out))
	return name, name != ""
}

func (e *Engine) kubeadmJoinCommand(n node.Node, baseJoin string, controlPlane bool, certKey string) string {
	parts := []string{"sudo", baseJoin}
	if controlPlane {
		parts = append(parts, "--control-plane", "--certificate-key", shellQuote(certKey))
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
	needsJoin, needsCerts := false, false
	for _, n := range e.nodes {
		if n != e.master && n.IsNew() && (n.IsControl() || n.IsWorker()) {
			needsJoin = true
			needsCerts = needsCerts || n.IsControl()
		}
	}
	if !needsJoin {
		return nil
	}
	if needsCerts && certKey == "" {
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
		if n.IsWorker() {
			if err := e.removeControlPlaneTaints(n.GetHostname()); err != nil {
				return err
			}
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

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func waitRetry(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
