package node

import (
	"encoding/base64"
)

type base struct {
	addr      string
	port      uint16
	isETCD    bool
	isControl bool
	isWorker  bool
	isNew     bool
	username  string
	password  string
	hostname  string
}

func (b *base) GetAddress() string {
	return b.addr
}

func (b *base) GetRole() []string {
	var role []string
	if b.isETCD {
		role = append(role, "etcd")
	}
	if b.isControl {
		role = append(role, "controlplane")
	}
	if b.isWorker {
		role = append(role, "worker")
	}
	return role
}

func (b *base) IsETCD() bool {
	return b.isETCD
}

func (b *base) IsControl() bool {
	return b.isControl
}

func (b *base) IsWorker() bool {
	return b.isWorker
}

func (b *base) GetPort() uint16 {
	return b.port
}

func (b *base) GetUsername() string {
	return base64.StdEncoding.EncodeToString([]byte(b.username))
}

func (b *base) GetPassword() string {
	return base64.StdEncoding.EncodeToString([]byte(b.password))
}

func (b *base) GetHostname() string {
	return b.hostname
}

func (b *base) SetHostname(hostname string) {
	b.hostname = hostname
}

func (b *base) SetIsNew(isNew bool) {
	b.isNew = isNew
}

func (b *base) IsNew() bool {
	return b.isNew
}
