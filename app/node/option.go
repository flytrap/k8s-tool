package node

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
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
