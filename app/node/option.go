package node

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"
)

type Option func(n *node) error

func Address(addr string) Option {
	return func(n *node) error {
		_, err := net.ResolveIPAddr("", addr)
		if err != nil {
			return err
		}
		n.addr = addr
		return nil
	}
}

func Port(port uint16) Option {
	return func(n *node) error {
		n.port = port
		return nil
	}
}

func Role(role []string) Option {
	return func(n *node) error {
		for _, s := range role {
			s = strings.ToLower(s)
			switch s {
			case "etcd":
				n.isETCD = true
			case "controlplane":
				n.isControl = true
			case "worker":
				n.isWorker = true
			default:
				return fmt.Errorf("invalid role: %s", s)
			}
		}
		return nil
	}
}

func Username(username string) Option {
	return func(n *node) error {
		buf, err := base64.StdEncoding.DecodeString(username)
		if err != nil {
			return err
		}
		n.username = strings.TrimSpace(string(buf))
		return nil
	}
}

func Password(password string) Option {
	return func(n *node) error {
		buf, err := base64.StdEncoding.DecodeString(password)
		if err != nil {
			return err
		}
		n.password = strings.TrimSpace(string(buf))
		return nil
	}
}

func KeyPath(keyPath string) Option {
	return func(n *node) error {
		n.keyPath = keyPath
		return nil
	}
}

// Context sets the lifetime inherited by all node operations.
func Context(ctx context.Context) Option {
	return func(n *node) error {
		if ctx == nil {
			return fmt.Errorf("context must not be nil")
		}
		n.ctx = ctx
		return nil
	}
}
func KnownHosts(path string) Option { return func(n *node) error { n.knownHosts = path; return nil } }

// InsecureSkipHostKeyCheck explicitly disables known_hosts and fingerprint checks.
// Verification remains enabled unless this option is set to true.
func InsecureSkipHostKeyCheck(skip bool) Option {
	return func(n *node) error {
		n.insecureSkipHostKeyCheck = skip
		return nil
	}
}

func HostKeyFingerprint(pin string) Option {
	return func(n *node) error {
		n.fingerprint = pin
		return nil
	}
}

func validateHostKeyFingerprint(pin string) error {
	if pin == "" {
		return nil
	}
	if !strings.HasPrefix(pin, "SHA256:") {
		return fmt.Errorf("host key fingerprint must use SHA256 format")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(pin, "SHA256:"))
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid SHA256 host key fingerprint")
	}
	return nil
}
func ConnectTimeout(d time.Duration) Option {
	return durationOption(d, func(n *node) { n.connectTimeout = d })
}
func CommandTimeout(d time.Duration) Option {
	return durationOption(d, func(n *node) { n.commandTimeout = d })
}
func TransferTimeout(d time.Duration) Option {
	return durationOption(d, func(n *node) { n.transferTimeout = d })
}
func durationOption(d time.Duration, set func(*node)) Option {
	return func(n *node) error {
		if d <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		set(n)
		return nil
	}
}

// ValidateHostname accepts Kubernetes DNS1123 subdomain names.
func ValidateHostname(name string) error {
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("invalid hostname %q: expected a DNS1123 name", name)
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid hostname %q: expected DNS1123 labels", name)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("invalid hostname %q: expected lowercase DNS1123 labels", name)
			}
		}
	}
	return nil
}
