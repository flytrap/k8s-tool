package engine

import (
	"errors"
	"fmt"
	"net"

	"k8s-tool/app/node"
	"k8s-tool/app/resources"
)

func (e *Engine) AddNode(n node.Node) error {
	if n == nil {
		return errors.New("node must not be nil")
	}
	for _, existing := range e.nodes {
		if n.GetAddress() == existing.GetAddress() {
			return fmt.Errorf("%s: node already in cluster", n.GetAddress())
		}
		if n.GetHostname() == existing.GetHostname() {
			return fmt.Errorf("%s: duplicate hostname", n.GetHostname())
		}
	}
	if n.IsControl() && e.master == nil {
		e.master = n
	}
	e.nodes = append(e.nodes, n)
	return nil
}

func (e *Engine) check() error {
	if len(e.nodes) == 0 {
		return errors.New("cluster must have at least one node")
	}
	if ip := net.ParseIP(e.vip); ip == nil || ip.To4() == nil {
		return errors.New("vip must be an IPv4 address")
	}
	if e.CRISocket != resources.DefaultCRISocket && e.CRISocket != "unix:///var/run/containerd/containerd.sock" {
		return errors.New("cri-socket must point to /run/containerd/containerd.sock")
	}
	if e.registry.hostname == "" {
		return errors.New("registry must not be empty")
	}
	var controls, workers int
	addresses, hostnames := map[string]bool{}, map[string]bool{}
	for _, n := range e.nodes {
		if err := node.ValidateHostname(n.GetHostname()); err != nil {
			return err
		}
		ip := net.ParseIP(n.GetAddress())
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%s: node address must be an IPv4 address", n.GetHostname())
		}
		if addresses[ip.String()] || hostnames[n.GetHostname()] {
			return fmt.Errorf("%s: duplicate node address or hostname", n.GetHostname())
		}
		addresses[ip.String()], hostnames[n.GetHostname()] = true, true
		if n.GetPort() == 0 {
			return fmt.Errorf("%s: SSH port must be greater than zero", n.GetHostname())
		}
		if n.IsETCD() != n.IsControl() {
			return fmt.Errorf("%s: etcd and controlplane roles must be colocated; independent etcd is not supported", n.GetHostname())
		}
		if !n.IsControl() && !n.IsWorker() {
			return fmt.Errorf("%s: node must have a controlplane or worker role", n.GetHostname())
		}
		if n.IsControl() {
			controls++
		}
		if n.IsWorker() {
			workers++
		}
		if e.ntp.server == n.GetAddress() && e.ntp.allow == "" {
			return errors.New("ntp allow is empty")
		}
	}
	if controls == 0 || workers == 0 {
		return errors.New("cluster needs at least one colocated etcd/controlplane and one worker (roles may share a node)")
	}
	return nil
}
