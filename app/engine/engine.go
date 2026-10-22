package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s-tool/app/node"
	"k8s-tool/app/resources"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	nodeJoinTimeout     = 5 * time.Minute
	istioInstallTimeout = 10 * time.Minute
)

type Engine struct {
	ctx               context.Context
	namespace         string
	KubernetesVersion string
	IstioVersion      string
	CRISocket         string
	vip               string
	region            string
	ntp               struct {
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
	e := &Engine{ctx: context.Background(), CRISocket: resources.DefaultCRISocket}
	e.registry.hostname = "registry.k8s.io"
	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, err
		}
	}
	if e.KubernetesVersion == "" || e.IstioVersion == "" {
		return nil, fmt.Errorf("Kubernetes and Istio versions must be configured")
	}
	return e, nil
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

func (e *Engine) installContainerd() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("containerd", resources.ContainerdVersion, resources.RuncVersion, resources.CNIVersion)
		})
	}
	return eg.Wait()
}

func (e *Engine) pullKubernetesImages() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsNew() {
			continue
		}
		eg.Go(func() error {
			return n.Install("kubeadm/images", e.KubernetesVersion, e.CRISocket, e.registry.hostname)
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
			return n.Install("kubeadm", e.KubernetesVersion)
		})
	}
	return eg.Wait()
}

func (e *Engine) installCalico() error {
	if err := e.master.Install("calico", resources.CalicoVersion); err != nil {
		return err
	}
	return e.waitForClusterNetworkReady()
}

func (e *Engine) installHelm() error {
	return e.master.Install("helm", resources.HelmVersion)
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
	return e.master.Install("nfs", e.nfs.server, e.nfs.path, e.namespace, resources.NFSChartVersion)
}

func (e *Engine) installIstio() error {
	if err := e.waitForClusterNetworkReady(); err != nil {
		return err
	}

	installErr := e.master.InstallWithTimeout("istio", istioInstallTimeout, e.IstioVersion)
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if installErr != nil {
		return fmt.Errorf("install Istio %s: %w", e.IstioVersion, installErr)
	}
	return e.ensureIstioReady(false)
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
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
	defer cancel()
	for {
		out, err := e.master.RunContext(ctx, "", "kubectl -n istio-system get endpoints istiod -o jsonpath='{.subsets[*].addresses[*].ip}'")
		if err == nil && strings.TrimSpace(string(out)) != "" {
			logrus.Infof("istiod endpoints: %s", strings.TrimSpace(string(out)))
			return nil
		}
		if ctx.Err() != nil {
			if err != nil {
				return fmt.Errorf("wait for istiod endpoints: %w", errors.Join(ctx.Err(), err))
			}
			return fmt.Errorf("wait for istiod endpoints: %w", ctx.Err())
		}
		if err := waitRetry(ctx); err != nil {
			return err
		}
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
	return e.master.Install("app", resources.MetricsServerVersion, resources.KubeStateMetricsChartVersion)
}

func (e *Engine) join() error {
	if err := e.joinNodes(""); err != nil {
		return err
	}
	return e.waitForClusterNetworkReady()
}
