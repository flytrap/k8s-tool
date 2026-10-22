package engine

import (
	"context"
	"errors"
)

type Option func(e *Engine) error

func Context(ctx context.Context) Option {
	return func(e *Engine) error {
		if ctx == nil {
			return errors.New("context must not be nil")
		}
		e.ctx = ctx
		return nil
	}
}

func Namespace(namespace string) Option {
	return func(e *Engine) error {
		e.namespace = namespace
		return nil
	}
}

func KubernetesVersion(version string) Option {
	return func(e *Engine) error {
		e.KubernetesVersion = version
		return nil
	}
}

func IstioVersion(version string) Option {
	return func(e *Engine) error {
		e.IstioVersion = version
		return nil
	}
}

func CRISocket(cRISocket string) Option {
	return func(e *Engine) error {
		e.CRISocket = cRISocket
		return nil
	}
}

func Vip(vip string) Option {
	return func(e *Engine) error {
		e.vip = vip
		return nil
	}
}

func Region(region string) Option {
	return func(e *Engine) error {
		e.region = region
		return nil
	}
}

func NTP(server, allow, timezone string) Option {
	return func(e *Engine) error {
		e.ntp.server = server
		e.ntp.allow = allow
		e.ntp.timezone = timezone
		return nil
	}
}

func NFS(server, path string) Option {
	return func(e *Engine) error {
		e.nfs.server = server
		e.nfs.path = path
		return nil
	}
}

func Registry(address string) Option {
	return func(e *Engine) error {
		e.registry.hostname = address
		return nil
	}
}
