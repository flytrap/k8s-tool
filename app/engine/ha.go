package engine

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"k8s-tool/app/node"
)

func (e *Engine) haBackends() []string {
	var backends []string
	for _, n := range e.nodes {
		if n.IsControl() {
			backends = append(backends, fmt.Sprintf("server %s %s check", n.GetHostname(), net.JoinHostPort(n.GetAddress(), "6443")))
		}
	}
	return backends
}

// Configure one node at a time; existing HA services remain available on peers.
func (e *Engine) installHa() error {
	backends := e.haBackends()
	for _, n := range e.haNodes() {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		if err := n.Install("haproxy", backends...); err != nil {
			return fmt.Errorf("%s: install haproxy: %w", n.GetHostname(), err)
		}
	}
	return nil
}

// New nodes first, existing backups next, the management node last.
func (e *Engine) haNodes() []node.Node {
	var fresh, existing []node.Node
	for _, n := range e.nodes {
		if !n.IsControl() || n == e.master {
			continue
		}
		if n.IsNew() {
			fresh = append(fresh, n)
		} else {
			existing = append(existing, n)
		}
	}
	out := append(fresh, existing...)
	if e.master != nil {
		out = append(out, e.master)
	}
	return out
}

func (e *Engine) configureKeepalived(n node.Node, stopBackup bool) error {
	state, priority := "BACKUP", 99
	if n == e.master {
		state, priority = "MASTER", 100
	} else {
		for _, peer := range e.nodes {
			if !peer.IsControl() || peer == e.master {
				continue
			}
			if peer == n {
				break
			}
			priority--
		}
		if priority < 1 {
			priority = 1
		}
	}
	var peers []string
	for _, peer := range e.nodes {
		if peer.IsControl() && peer != n {
			peers = append(peers, peer.GetAddress())
		}
	}
	if err := n.Install("keepalived", e.vip, state, n.GetAddress(), strings.Join(peers, ","), strconv.Itoa(priority), e.region); err != nil {
		return fmt.Errorf("%s: install keepalived: %w", n.GetHostname(), err)
	}
	if stopBackup && n != e.master {
		return n.StopService("keepalived")
	}
	return nil
}

func (e *Engine) installKeepalived() error {
	for _, n := range e.haNodes() {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		if err := e.configureKeepalived(n, true); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) startKeepalivedBackups() error {
	for _, n := range e.haNodes() {
		if n == e.master {
			continue
		}
		if err := e.ctx.Err(); err != nil {
			return err
		}
		if err := n.StartService("keepalived"); err != nil {
			return fmt.Errorf("%s: start keepalived: %w", n.GetHostname(), err)
		}
	}
	return nil
}

// This is also safe to retry after a completed join: IsNew is not a prerequisite.
func (e *Engine) syncHA() error {
	// Membership does not tell us whether HA is already running: a node may
	// have joined in a previous process before HA synchronization was retried.
	// Refresh all live peers before starting any additional VRRP participant.
	var active, inactive []node.Node
	for _, n := range e.haNodes() {
		ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
		out, err := n.RunContext(ctx, "", keepalivedStatusCommand)
		cancel()
		if err != nil {
			return fmt.Errorf("%s: check keepalived: %w", n.GetHostname(), err)
		}
		switch strings.TrimSpace(string(out)) {
		case "active":
			active = append(active, n)
		case "inactive":
			inactive = append(inactive, n)
		default:
			return fmt.Errorf("%s: unexpected keepalived status", n.GetHostname())
		}
	}
	if len(active) == 0 {
		return fmt.Errorf("no active keepalived instance; restore the existing cluster VIP before synchronizing HA")
	}
	if err := e.installHa(); err != nil {
		return err
	}
	for _, n := range append(active, inactive...) {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		// Do not stop backups during expansion, including backups that already existed.
		if err := e.configureKeepalived(n, false); err != nil {
			return err
		}
	}
	return nil
}

const keepalivedStatusCommand = "if systemctl is-active --quiet keepalived; then printf active; else printf inactive; fi"
