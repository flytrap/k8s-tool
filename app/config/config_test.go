package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMustLoadCRISocket(t *testing.T) {
	once = sync.Once{}
	C = new(Config)

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("cri-socket: unix:///var/run/containerd/containerd.sock\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	MustLoad(path)

	want := "unix:///var/run/containerd/containerd.sock"
	if C.CRISocket != want {
		t.Fatalf("CRISocket = %q, want %q", C.CRISocket, want)
	}
}
