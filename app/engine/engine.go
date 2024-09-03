package engine

import (
	"errors"
	"fmt"
	"k8s-tool/app/node"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
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

	if e.master == nil && n.IsControl() {
		e.master = n
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
	if len(steps) > 0 {
		li := strings.Split(steps, ",")
		if li[0] != "1" {
			li = append([]string{"1"}, li...)
		}
		for _, i := range li {
			n, err := strconv.Atoi(i)
			if err != nil {
				continue
			}
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
	if len(steps) > 0 {
		li := strings.Split(steps, ",")
		if li[0] != "1" {
			li = append([]string{"1"}, li...)
		}
		for _, i := range li {
			n, err := strconv.Atoi(i)
			if err != nil {
				continue
			}
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

func (e *Engine) checkNew() error {
	res, err := e.master.Run("", "kubectl get nodes|awk '{print $1}'")
	if err != nil {
		return err
	}
	nodeMap := map[string]string{}
	for _, n := range strings.Split(string(res), "\n") {
		nodeMap[n] = n
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
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.IsETCD() {
			continue
		}
		state := "BACKUP"
		if n == e.master {
			state = "MASTER"
		}
		eg.Go(func() error {
			err := n.Install("keepalived", e.vip, state, n.GetAddress())
			if err != nil {
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
	res, err := e.master.Run(filepath.Join("resource", "kubeadm"), fmt.Sprint("bash", " ", "start.sh", " ", e.vip, " ", e.master.GetHostname(), " ", e.master.GetAddress()))
	if err != nil {
		return err
	}
	logrus.Info(res)
	r := regexp.MustCompile("(kubeadm join.*?\n.*?\n.*?)\n")
	js := r.FindAll(res, -1)
	e.master.Run(filepath.Join("resource", "kubeadm"), fmt.Sprint("bash", " ", "config.sh"))
	e.master.Run("", fmt.Sprint("kubectl taint node ", e.master.GetHostname(), " node-role.kubernetes.io/master-"))
	li := strings.Split(string(js[0]), "\\\n")
	nj := fmt.Sprintf("sudo %s %s", li[0], li[1])
	mj := fmt.Sprintf("sudo %s %s %s", li[0], li[1], li[2])
	logrus.Info(mj)
	logrus.Info(nj)
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		if n == e.master {
			continue
		}
		eg.Go(func() error {
			if n.IsControl() {
				_, err = n.Run("", mj)
				if err != nil {
					return err
				}
				n.Run(filepath.Join("resource", "kubeadm"), fmt.Sprint("bash", " ", "config.sh"))
				n.Run("", fmt.Sprint("kubectl taint ", n.GetHostname(), " node-role.kubernetes.io/control-plane-"))
			} else {
				_, err = n.Run("", nj)
			}
			return err
		})
	}
	return eg.Wait()
}

func (e *Engine) installCalico() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		eg.Go(func() error {
			return n.Install("calico")
		})
	}
	err := eg.Wait()
	if err != nil {
		return err
	}
	_, err = e.master.Run(filepath.Join("resource", "calico"), "kubectl apply -f calico.yaml")
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
	eg := errgroup.Group{}
	for i := range e.nodes {
		n := e.nodes[i]
		eg.Go(func() error {
			return n.Install("istio/images") // 加载镜像
		})
	}
	err := eg.Wait()
	if err != nil {
		return err
	}
	return e.master.Install("istio")
}

func (e *Engine) installApp() error {
	var eg errgroup.Group
	for i := range e.nodes {
		n := e.nodes[i]
		eg.Go(func() error {
			return n.Install("app/images")
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	e.master.Install("app")
	return nil
}

func (e *Engine) join() error {
	mj, err := e.master.Run("", "sudo kubeadm token create --print-join-command --certificate-key $(kubeadm certs certificate-key)")
	if err != nil {
		return err
	}
	nj, err := e.master.Run("", "sudo kubeadm token create --print-join-command")
	if err != nil {
		return err
	}

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
			if err := n.Install("app/images"); err != nil {
				return err
			}
			if n.IsControl() {
				_, err = n.Run("", string(mj))
				if err != nil {
					e.master.Run("", "sudo kubeadm init phase upload-certs --upload-certs")
					mj, err = e.master.Run("", "sudo kubeadm token create --print-join-command --certificate-key $(kubeadm certs certificate-key)")
					if err != nil {
						return err
					}
					_, err = n.Run("", strings.Join([]string{"sudo", strings.TrimSpace(string(mj)), e.CRISocket}, " "))
				}
			} else {
				_, err = n.Run("", strings.Join([]string{"sudo", strings.TrimSpace(string(nj)), e.CRISocket}, " "))
			}
			return err
		})
	}
	return eg.Wait()
}
