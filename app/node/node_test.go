package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"k8s-tool/app/resources"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestHostsValidationAndExactMatching(t *testing.T) {
	for _, name := range []string{"$(touch /tmp/injected)", "node;id", "a'", "A", "-node", "node..test", "a\nnode"} {
		if _, err := hostsCommand("replace", "127.0.0.1", name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := hostsCommand("add", "127.0.0.1; id", "node"); err == nil {
		t.Fatal("accepted invalid IP")
	}
	// Execute the actual generated commands against an isolated hosts fixture.
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	sudo := filepath.Join(dir, "sudo")
	if err := os.WriteFile(sudo, []byte("#!/bin/sh\nexec \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	original := "127.0.0.1 localhost\n10.0.0.1 node-long\n10.0.0.2 old node # alias\n10.0.0.3 another # node\n  # keep comment node\n"
	for _, mode := range []string{"add", "replace", "remove"} {
		if err := os.WriteFile(hosts, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		cmd, err := hostsCommand(mode, "10.0.0.9", "node")
		if err != nil {
			t.Fatal(err)
		}
		cmd = strings.ReplaceAll(cmd, "/etc/hosts", hosts)
		c := exec.Command("sh", "-c", cmd)
		c.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", mode, err, out)
		}
		b, _ := os.ReadFile(hosts)
		out := string(b)
		if !strings.Contains(out, "node-long") || !strings.Contains(out, "another # node") || !strings.Contains(out, "  # keep comment node") {
			t.Fatalf("inexact match: %s", out)
		}
		if mode == "add" && out != original {
			t.Fatalf("add duplicated existing alias: %s", out)
		}
		if mode != "add" && strings.Contains(out, "old node") {
			t.Fatalf("alias retained: %s", out)
		}
		if mode == "replace" && !strings.Contains(out, "10.0.0.9  node") {
			t.Fatal(out)
		}
	}
}
func TestShellEscape(t *testing.T) {
	value := "a'$(printf hostile); b\nline"
	out, err := exec.Command("sh", "-c", "printf %s "+shellEscape(value)).Output()
	if err != nil || string(out) != value {
		t.Fatalf("%q %v", out, err)
	}
}

func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestHostKeyVerification(t *testing.T) {
	key := signer(t)
	other := signer(t)
	addr := "[127.0.0.1]:2222"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{addr}, key.PublicKey())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	instance, err := New(KnownHosts(path))
	if err != nil {
		t.Fatal(err)
	}
	n := instance.(*node)
	cb, err := n.hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if err := cb(addr, remote, key.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if cb(addr, remote, other.PublicKey()) == nil {
		t.Fatal("accepted changed key")
	}
	if cb("[127.0.0.2]:2222", remote, key.PublicKey()) == nil {
		t.Fatal("accepted unknown host")
	}
	instance, err = New(HostKeyFingerprint(ssh.FingerprintSHA256(key.PublicKey())), KnownHosts("missing"))
	if err != nil {
		t.Fatal(err)
	}
	cb, err = instance.(*node).hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if cb(addr, remote, key.PublicKey()) != nil || cb(addr, remote, other.PublicKey()) == nil {
		t.Fatal("pin verification failed")
	}
	if _, err := New(HostKeyFingerprint("SHA256:bad")); err == nil {
		t.Fatal("accepted malformed pin")
	}
}

func TestInsecureSkipHostKeyCheck(t *testing.T) {
	key, other := signer(t), signer(t)
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	for _, opts := range [][]Option{
		{InsecureSkipHostKeyCheck(true), KnownHosts(filepath.Join(t.TempDir(), "missing"))},
		{HostKeyFingerprint(ssh.FingerprintSHA256(key.PublicKey())), InsecureSkipHostKeyCheck(true)},
		{InsecureSkipHostKeyCheck(true), HostKeyFingerprint("ignored malformed fingerprint")},
		{HostKeyFingerprint("ignored malformed fingerprint"), InsecureSkipHostKeyCheck(true)},
	} {
		instance, err := New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		callback, err := instance.(*node).hostKeyCallback()
		if err != nil {
			t.Fatal(err)
		}
		for _, serverKey := range []ssh.PublicKey{key.PublicKey(), other.PublicKey()} {
			if err := callback("unknown.example:2222", remote, serverKey); err != nil {
				t.Fatalf("explicit skip still verified host identity: %v", err)
			}
		}
	}
	// Explicit false must retain validation even when a previous option enabled it.
	if _, err := New(InsecureSkipHostKeyCheck(true), HostKeyFingerprint("invalid"), InsecureSkipHostKeyCheck(false)); err == nil {
		t.Fatal("explicit false did not restore fingerprint validation")
	}
}

// sshFixture can stall before authentication, channel-open, or exec completion.
func sshFixture(t *testing.T, mode string) (string, ssh.Signer) {
	t.Helper()
	key := signer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var connections []net.Conn
	var workers sync.WaitGroup
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, c := range connections {
			c.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, raw)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer raw.Close()
				if mode == "handshake" {
					io.Copy(io.Discard, raw)
					return
				}
				conf := &ssh.ServerConfig{NoClientAuth: true}
				conf.AddHostKey(key)
				conn, chans, reqs, err := ssh.NewServerConn(raw, conf)
				if err != nil {
					return
				}
				defer conn.Close()
				handlers := sftp.InMemHandler()
				go ssh.DiscardRequests(reqs)
				for incoming := range chans {
					if mode == "channel" {
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							if mode == "sftp-online" && req.Type == "subsystem" {
								req.Reply(true, nil)
								server := sftp.NewRequestServer(channel, handlers)
								defer server.Close()
								server.Serve()
								return
							}
							if mode == "sftp" && req.Type == "subsystem" {
								req.Reply(true, nil)
								conn.Wait()
								return
							}
							if req.Type != "exec" {
								req.Reply(false, nil)
								continue
							}
							req.Reply(true, nil)
							var payload struct{ Command string }
							ssh.Unmarshal(req.Payload, &payload)
							switch payload.Command {
							case "cat /etc/os-release | grep -w ID | awk -F '=' '{print $2}'":
								io.WriteString(channel, "ubuntu\n")
							case "arch":
								io.WriteString(channel, "x86_64\n")
							case "echo -n $HOME":
								io.WriteString(channel, "/root")
							default:
								if mode == "exec" {
									conn.Wait()
									return
								}
								io.WriteString(channel, "ok")
							}
							channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	return listener.Addr().String(), key
}
func fixtureNode(t *testing.T, addr string, key ssh.Signer, opts ...Option) *node {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	var p uint16
	fmt.Sscan(port, &p)
	base := []Option{Address(host), Port(p), HostKeyFingerprint(ssh.FingerprintSHA256(key.PublicKey()))}
	n, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n.(*node)
}
func TestHandshakeTimeout(t *testing.T) {
	addr, key := sshFixture(t, "handshake")
	n := fixtureNode(t, addr, key, ConnectTimeout(80*time.Millisecond))
	start := time.Now()
	err := n.Connect()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("handshake did not stop")
	}
}

func TestConnectWithHostKeyVerificationSkipped(t *testing.T) {
	addr, _ := sshFixture(t, "normal")
	// Pin a different key and point at a missing known_hosts file. Both are
	// deliberately ignored only because the caller explicitly enabled skipping.
	n := fixtureNode(t, addr, signer(t), KnownHosts(filepath.Join(t.TempDir(), "missing")), InsecureSkipHostKeyCheck(true))
	if err := n.Connect(); err != nil {
		t.Fatalf("explicit skip did not allow connection: %v", err)
	}
	if _, err := n.Run("", "true"); err != nil {
		t.Fatal(err)
	}
}
func TestCommandAndChannelTimeout(t *testing.T) {
	for _, mode := range []string{"exec", "channel"} {
		t.Run(mode, func(t *testing.T) {
			addr, key := sshFixture(t, mode)
			n := fixtureNode(t, addr, key, CommandTimeout(80*time.Millisecond))
			if mode == "exec" {
				if err := n.Connect(); err != nil {
					t.Fatal(err)
				}
				_, err := n.Run("", "secret password")
				if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unexpected timeout: %v", err)
				}
			} else {
				if err := n.Connect(); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("channel-open: %v", err)
				}
			}
		})
	}
}
func TestCommandCancellationAndReconnect(t *testing.T) {
	addr, key := sshFixture(t, "exec")
	n := fixtureNode(t, addr, key)
	if err := n.Connect(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	_, err := n.RunContext(ctx, "", "hang")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := n.ConnectContext(context.Background()); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
}

func TestSFTPInitializationTimeout(t *testing.T) {
	addr, key := sshFixture(t, "sftp")
	n := fixtureNode(t, addr, key, TransferTimeout(80*time.Millisecond))
	if err := n.Connect(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := n.ReadFile("missing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected transfer deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("SFTP initialization did not stop")
	}
}

func TestNestedComponentPaths(t *testing.T) {
	instance, err := New()
	if err != nil {
		t.Fatal(err)
	}
	n := instance.(*node)
	for _, name := range []string{"containerd", "kubeadm/images", "nfs/nfs-utils"} {
		if _, _, err := n.componentPaths(name); err != nil {
			t.Fatalf("rejected nested component %q: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "../outside", "/outside", "docker/../outside", `docker\images`} {
		if _, _, err := n.componentPaths(name); err == nil {
			t.Fatalf("accepted unsafe component %q", name)
		}
	}
}

func TestStageUsesEmbeddedOnlineScripts(t *testing.T) {
	addr, key := sshFixture(t, "sftp-online")
	n := fixtureNode(t, addr, key)
	n.stdout = io.Discard
	if err := n.Connect(); err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"containerd", "kubeadm/images", "istio"} {
		if err := n.Stage(component); err != nil {
			t.Fatalf("stage %s: %v", component, err)
		}
		name := component + "/install.sh"
		want, err := fs.ReadFile(resources.Files(), name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := n.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("staged script differs from bundled script: %s", name)
		}
	}
	for _, name := range []string{"common.sh", "istio/conf/loki/telemetry.yaml"} {
		got, err := n.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		want, err := fs.ReadFile(resources.Files(), name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("missing shared or nested embedded resource %s", name)
		}
	}
	if err := n.Stage("docker"); err == nil {
		t.Fatal("legacy Docker component remains available")
	}
}
