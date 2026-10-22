package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s-tool/app/resources"
)

func configFile(t *testing.T, contents string) string {
	t.Helper()
	if !strings.Contains(contents, "kubernetes-version:") {
		contents = "kubernetes-version: v1.37.1\n" + contents
	}
	if !strings.Contains(contents, "istio-version:") {
		contents = "istio-version: 1.31.2\n" + contents
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLoadDefaultsAndCRISocket(t *testing.T) {
	c, err := Load(configFile(t, "cri-socket: unix:///var/run/containerd/containerd.sock\nnodes:\n  - address: 192.0.2.1\n    hostname: master1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.CRISocket != "unix:///var/run/containerd/containerd.sock" || c.Nodes[0].Port != 22 || c.Namespace != "default" || c.SSH.ConnectTimeout != 30*time.Second || c.SSH.CommandTimeout != 15*time.Minute || c.SSH.TransferTimeout != 30*time.Minute {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.SSH.InsecureSkipHostKeyCheck {
		t.Fatal("host verification must be enabled by default")
	}
}

func TestLoadInsecureSkipHostKeyCheck(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		c, err := Load(configFile(t, "ssh:\n  insecure-skip-host-key-check: "+value+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if c.SSH.InsecureSkipHostKeyCheck != (value == "true") {
			t.Fatalf("incorrect skip setting for %s", value)
		}
	}
}
func TestLoadIndependentConfigurations(t *testing.T) {
	first, err := Load(configFile(t, "namespace: first\nregion: first-only\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(configFile(t, "namespace: second\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Namespace != "first" || second.Namespace != "second" || second.Region != "" {
		t.Fatal("configuration leaked between loads")
	}
}
func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, content := range []string{"nodes: [null]", "ssh:\n  connect-timeout: -1s", "ssh:\n  transfer-timeout: invalid", "unknown-setting: true", "nodes:\n - address: 192.0.2.1\n   hostname: $(id)", "nodes:\n - address: invalid\n   hostname: node"} {
		t.Run(content, func(t *testing.T) {
			if _, err := Load(configFile(t, content)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("unexpected missing config error: %v", err)
	}
}

func TestVersionsRequiredAndConfigurable(t *testing.T) {
	c, err := Load(configFile(t, "namespace: default\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.KubernetesVersion != "v1.37.1" || c.IstioVersion != "1.31.2" || c.CRISocket != resources.DefaultCRISocket || c.Registry != "registry.k8s.io" {
		t.Fatalf("incorrect component defaults: %+v", c)
	}
	custom, err := Load(configFile(t, "kubernetes-version: v1.29.2\nistio-version: 1.20.3\n"))
	if err != nil || custom.KubernetesVersion != "v1.29.2" || custom.IstioVersion != "1.20.3" {
		t.Fatalf("valid configured versions were not used: %+v, %v", custom, err)
	}
	for _, contents := range []string{"kubernetes-version: latest", "kubernetes-version: 1.37.1", "istio-version: v1.31.2", "istio-version: latest", "cri-socket: unix:///var/run/cri-dockerd.sock"} {
		if _, err := Load(configFile(t, contents)); err == nil {
			t.Fatalf("accepted incompatible pin/socket %q", contents)
		}
	}
	missingVersionFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(missingVersionFile, []byte("nodes: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(missingVersionFile); err == nil {
		t.Fatal("accepted a config without explicit Kubernetes and Istio versions")
	}
}
