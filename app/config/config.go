package config

import (
	"fmt"
	"net"
	"regexp"
	"time"

	"k8s-tool/app/node"
	"k8s-tool/app/resources"

	"github.com/spf13/viper"
)

// Load uses a fresh parser for every invocation and never changes global state.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetDefault("namespace", "default")
	v.SetDefault("registry", "registry.k8s.io")
	v.SetDefault("cri-socket", resources.DefaultCRISocket)
	v.SetDefault("ssh.insecure-skip-host-key-check", false)
	v.SetDefault("ssh.connect-timeout", "30s")
	v.SetDefault("ssh.command-timeout", "15m")
	v.SetDefault("ssh.transfer-timeout", "30m")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read configuration %q: %w", path, err)
	}
	var c Config
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if c.SSH.ConnectTimeout <= 0 || c.SSH.CommandTimeout <= 0 || c.SSH.TransferTimeout <= 0 {
		return nil, fmt.Errorf("SSH timeouts must be positive durations")
	}
	if !kubernetesVersionPattern.MatchString(c.KubernetesVersion) {
		return nil, fmt.Errorf("kubernetes-version must be a stable v1.x.y release, got %q", c.KubernetesVersion)
	}
	if !istioVersionPattern.MatchString(c.IstioVersion) {
		return nil, fmt.Errorf("istio-version must be a stable 1.x.y release, got %q", c.IstioVersion)
	}
	if c.CRISocket != resources.DefaultCRISocket && c.CRISocket != "unix:///var/run/containerd/containerd.sock" {
		return nil, fmt.Errorf("cri-socket must point to /run/containerd/containerd.sock; Docker/CRI-dockerd migration requires a separate procedure")
	}
	for i, n := range c.Nodes {
		if n == nil {
			return nil, fmt.Errorf("nodes[%d] must not be null", i)
		}
		if n.Port == 0 {
			n.Port = 22
		}
		if net.ParseIP(n.Address) == nil {
			return nil, fmt.Errorf("nodes[%d].address must be an IP address", i)
		}
		if err := node.ValidateHostname(n.Hostname); err != nil {
			return nil, fmt.Errorf("nodes[%d]: %w", i, err)
		}
	}
	return &c, nil
}

type ntpConfig struct {
	Server   string `mapstructure:"server"`
	Allow    string `mapstructure:"allow"`
	Timezone string `mapstructure:"timezone"`
}
type nfsConfig struct {
	Server string `mapstructure:"server"`
	Path   string `mapstructure:"path"`
}
type NodeConfig struct {
	Address            string   `mapstructure:"address"`
	Hostname           string   `mapstructure:"hostname"`
	Role               []string `mapstructure:"role"`
	Port               uint16   `mapstructure:"port"`
	Username           string   `mapstructure:"username"`
	Password           string   `mapstructure:"password"`
	KeyPath            string   `mapstructure:"keyPath"`
	HostKeyFingerprint string   `mapstructure:"host-key-fingerprint"`
}
type SSHConfig struct {
	InsecureSkipHostKeyCheck bool          `mapstructure:"insecure-skip-host-key-check"`
	KnownHosts               string        `mapstructure:"known-hosts"`
	ConnectTimeout           time.Duration `mapstructure:"connect-timeout"`
	CommandTimeout           time.Duration `mapstructure:"command-timeout"`
	TransferTimeout          time.Duration `mapstructure:"transfer-timeout"`
}
type Config struct {
	KubernetesVersion string        `mapstructure:"kubernetes-version"`
	IstioVersion      string        `mapstructure:"istio-version"`
	Namespace         string        `mapstructure:"namespace"`
	Registry          string        `mapstructure:"registry"`
	CRISocket         string        `mapstructure:"cri-socket"`
	Vip               string        `mapstructure:"vip"`
	Region            string        `mapstructure:"region"`
	NTP               ntpConfig     `mapstructure:"ntp"`
	NFS               nfsConfig     `mapstructure:"nfs"`
	SSH               SSHConfig     `mapstructure:"ssh"`
	Nodes             []*NodeConfig `mapstructure:"nodes"`
}

var (
	kubernetesVersionPattern = regexp.MustCompile(`^v1\.[0-9]+\.[0-9]+$`)
	istioVersionPattern      = regexp.MustCompile(`^1\.[0-9]+\.[0-9]+$`)
)
