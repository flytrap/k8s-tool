package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

type clusterNode struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		NodeInfo struct {
			KubeletVersion          string `json:"kubeletVersion"`
			ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
		} `json:"nodeInfo"`
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
	} `json:"status"`
}

func (n clusterNode) controlPlane() bool {
	_, control := n.Metadata.Labels["node-role.kubernetes.io/control-plane"]
	_, legacy := n.Metadata.Labels["node-role.kubernetes.io/master"]
	return control || legacy
}

func (n clusterNode) matchesAddress(addr string) bool {
	want := net.ParseIP(addr)
	for _, a := range n.Status.Addresses {
		if a.Type == "InternalIP" && want.Equal(net.ParseIP(a.Address)) {
			return true
		}
	}
	return false
}

func (e *Engine) checkNew() error {
	// A new control-plane may precede every existing node in the configuration.
	// Only a candidate registered as a control-plane with this identity is trusted
	// as the management entry point.
	var failures []string
	for _, candidate := range e.nodes {
		if !candidate.IsControl() {
			continue
		}
		if err := e.ctx.Err(); err != nil {
			return err
		}
		out, err := candidate.RunContext(e.ctx, "", "kubectl get nodes -o json --request-timeout=15s")
		if err != nil {
			failures = append(failures, candidate.GetHostname()+": kubectl unavailable")
			continue
		}
		var inventory struct {
			Items []clusterNode `json:"items"`
		}
		if err := json.Unmarshal(out, &inventory); err != nil {
			failures = append(failures, candidate.GetHostname()+": invalid node inventory")
			continue
		}
		registered := map[string]clusterNode{}
		for _, n := range inventory.Items {
			registered[n.Metadata.Name] = n
		}
		self, ok := registered[candidate.GetHostname()]
		if !ok || !self.controlPlane() || !self.matchesAddress(candidate.GetAddress()) {
			failures = append(failures, candidate.GetHostname()+": not a registered control-plane with matching address")
			continue
		}
		versionJSON, err := candidate.RunContext(e.ctx, "", "kubectl version -o json --request-timeout=15s")
		if err != nil {
			return fmt.Errorf("read cluster Kubernetes version: %w", err)
		}
		var version struct {
			ServerVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"serverVersion"`
		}
		if err := json.Unmarshal(versionJSON, &version); err != nil {
			return fmt.Errorf("invalid cluster version response: %w", err)
		}
		if version.ServerVersion.GitVersion != e.KubernetesVersion {
			return fmt.Errorf("existing API server uses %q; expansion requires %s; upgrade the cluster separately", version.ServerVersion.GitVersion, e.KubernetesVersion)
		}
		for _, old := range inventory.Items {
			if old.Status.NodeInfo.KubeletVersion != e.KubernetesVersion {
				return fmt.Errorf("%s: existing kubelet uses %q; expansion requires %s; upgrade the cluster separately", old.Metadata.Name, old.Status.NodeInfo.KubeletVersion, e.KubernetesVersion)
			}
			if !strings.HasPrefix(old.Status.NodeInfo.ContainerRuntimeVersion, "containerd://2.") {
				return fmt.Errorf("%s: existing runtime is %q; migrate to containerd 2.x separately before expansion", old.Metadata.Name, old.Status.NodeInfo.ContainerRuntimeVersion)
			}
		}
		configured := map[string]bool{}
		for _, n := range e.nodes {
			if n.IsControl() {
				configured[n.GetHostname()] = true
			}
		}
		for _, n := range inventory.Items {
			if n.controlPlane() && !configured[n.Metadata.Name] {
				return fmt.Errorf("expansion configuration must include existing control-plane %q", n.Metadata.Name)
			}
		}
		for _, n := range e.nodes {
			if old, exists := registered[n.GetHostname()]; exists {
				if !old.matchesAddress(n.GetAddress()) || old.controlPlane() != n.IsControl() {
					return fmt.Errorf("%s: configured address or role differs from registered node", n.GetHostname())
				}
			} else {
				for _, old := range inventory.Items {
					if old.matchesAddress(n.GetAddress()) {
						return fmt.Errorf("%s: address already registered as %q", n.GetHostname(), old.Metadata.Name)
					}
				}
			}
		}
		for _, n := range e.nodes {
			_, exists := registered[n.GetHostname()]
			n.SetIsNew(!exists)
		}
		e.master = candidate
		return nil
	}
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if len(failures) == 0 {
		return errors.New("expansion requires an existing control-plane")
	}
	return fmt.Errorf("cannot discover existing control-plane: %s", strings.Join(failures, "; "))
}
