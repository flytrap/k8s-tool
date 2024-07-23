package engine

type Option func(e *Engine) error

func Namespace(namespace string) Option {
	return func(e *Engine) error {
		e.namespace = namespace
		return nil
	}
}

func Vip(vip string) Option {
	return func(e *Engine) error {
		e.vip = vip
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
