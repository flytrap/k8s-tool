package node

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s-tool/app/resources"

	"github.com/cheggaaa/pb"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const sftpMaxPacket = 1 << 15

var sftpPackets = sync.Pool{
	New: func() any {
		buf := make([]byte, sftpMaxPacket)
		return &buf
	},
}

type (
	Node interface {
		GetAddress() string
		GetRole() []string
		IsControl() bool
		IsWorker() bool
		IsETCD() bool
		GetPort() uint16
		GetUsername() string
		GetPassword() string
		GetHostname() string
		SetHostname(hostname string)
		SetIsNew(isNew bool)
		IsNew() bool
		Connect() error
		ConnectContext(context.Context) error
		Close()
		RunContext(context.Context, string, ...string) ([]byte, error)
		Stage(string) error
		AddHost(addr, name string) error
		RemoveHost(name string) error
		ReplaceHost(addr, name string) error
		Install(name string, a ...string) error
		InstallWithTimeout(name string, timeout time.Duration, a ...string) error
		ReadFile(path string) ([]byte, error)
		StopService(name string) error
		StartService(name string) error
		Run(cwd string, cmds ...string) ([]byte, error)
	}

	node struct {
		base
		stdout                                          io.Writer
		stderr                                          io.Writer
		sshcli                                          *ssh.Client
		os                                              string
		arch                                            string
		home                                            string
		ctx                                             context.Context
		knownHosts, fingerprint                         string
		insecureSkipHostKeyCheck                        bool
		connectTimeout, commandTimeout, transferTimeout time.Duration
		mu                                              sync.Mutex
	}
)

func New(opts ...Option) (Node, error) {
	n := &node{ctx: context.Background(), connectTimeout: 30 * time.Second, commandTimeout: 15 * time.Minute, transferTimeout: 30 * time.Minute}
	n.addr = "127.0.0.1"
	n.port = 22
	n.username = "root"
	n.stdout = os.Stdout
	n.stderr = os.Stderr
	n.isNew = true
	for _, opt := range opts {
		if err := opt(n); err != nil {
			return nil, err
		}
	}
	if !n.insecureSkipHostKeyCheck {
		if err := validateHostKeyFingerprint(n.fingerprint); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func (n *node) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if n.insecureSkipHostKeyCheck {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if n.fingerprint != "" {
		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(key)), []byte(n.fingerprint)) != 1 {
				return errors.New("SSH host key fingerprint mismatch")
			}
			return nil
		}, nil
	}
	path := n.knownHosts
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load SSH known_hosts %q: %w", path, err)
	}
	return callback, nil
}
func (n *node) Connect() error { return n.ConnectContext(n.ctx) }
func (n *node) ConnectContext(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, n.connectTimeout)
	defer cancel()
	callback, err := n.hostKeyCallback()
	if err != nil {
		return err
	}
	auth := []ssh.AuthMethod{ssh.Password(n.password)}
	if n.keyPath != "" {
		key, err := os.ReadFile(n.keyPath)
		if err != nil {
			return fmt.Errorf("read private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return fmt.Errorf("parse private key: %w", err)
		}
		auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	addr := net.JoinHostPort(n.addr, fmt.Sprint(n.port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("SSH connect: %w", err)
	}
	// The socket deadline bounds key exchange and authentication as well as dialing.
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	watcherDone := make(chan struct{})
	stopWatcher := context.AfterFunc(ctx, func() { conn.Close(); close(watcherDone) })
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{User: n.username, Auth: auth, HostKeyCallback: callback})
	if !stopWatcher() {
		<-watcherDone
	}
	if err != nil {
		conn.Close()
		if ctx.Err() != nil {
			return fmt.Errorf("SSH handshake: %w", ctx.Err())
		}
		return fmt.Errorf("SSH handshake: %w", err)
	}
	if ctx.Err() != nil {
		conn.Close()
		return fmt.Errorf("SSH handshake: %w", ctx.Err())
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(cc, chans, reqs)
	n.mu.Lock()
	old := n.sshcli
	n.sshcli = client
	n.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if err := n.fetchOSContext(parent); err != nil {
		n.Close()
		return err
	}
	if err := n.fetchArchContext(parent); err != nil {
		n.Close()
		return err
	}
	if err := n.fetchHomeContext(parent); err != nil {
		n.Close()
		return err
	}
	return nil
}
func (n *node) Close() {
	n.mu.Lock()
	client := n.sshcli
	n.sshcli = nil
	n.mu.Unlock()
	if client != nil {
		client.Close()
	}
}
func (n *node) client() (*ssh.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sshcli == nil {
		return nil, errors.New("SSH connection unavailable; reconnect before retrying")
	}
	return n.sshcli, nil
}

// bounded closes the transport on cancellation, including a blocked channel open.
// Wait for the operation before accessing its buffers or returning to the caller.
func (n *node) bounded(ctx context.Context, client *ssh.Client, operation string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		client.Close()
		<-done
		return fmt.Errorf("%s: %w (SSH connection closed; reconnect before retrying)", operation, ctx.Err())
	}
}
func (n *node) outputContext(ctx context.Context, cmd string) (string, error) {
	b, err := n.RunContext(ctx, "", cmd)
	return string(b), err
}

func (n *node) fetchOSContext(ctx context.Context) error {
	os, err := n.outputContext(ctx, "cat /etc/os-release | grep -w ID | awk -F '=' '{print $2}'")
	if err != nil {
		return err
	}

	os = strings.Trim(os, "\"\n")
	switch os {
	case "ubuntu", "debian":
		n.os = os
	default:
		return fmt.Errorf("unsupported operation system %q", os)
	}
	logrus.Infof("Node %s os: %s", n.addr, n.os)
	return nil
}

func (n *node) fetchArchContext(ctx context.Context) error {
	arch, err := n.outputContext(ctx, "arch")
	if err != nil {
		return err
	}

	arch = strings.TrimSpace(arch)
	switch arch {
	case "x86_64", "aarch64":
		n.arch = arch
	default:
		return fmt.Errorf("unsupported machine architecture: %q", arch)
	}
	logrus.Infof("Node %s arch: %s", n.addr, n.arch)
	return nil
}

func (n *node) fetchHomeContext(ctx context.Context) error {
	home, err := n.outputContext(ctx, "echo -n $HOME")
	if err != nil {
		return err
	}

	n.home = home
	logrus.Infof("Node %s home: %s", n.addr, n.home)
	return nil
}

func (n *node) AddHost(addr, name string) error     { return n.hosts("add", addr, name) }
func (n *node) RemoveHost(name string) error        { return n.hosts("remove", "", name) }
func (n *node) ReplaceHost(addr, name string) error { return n.hosts("replace", addr, name) }
func (n *node) hosts(mode, addr, name string) error {
	cmd, err := hostsCommand(mode, addr, name)
	if err != nil {
		return err
	}
	info, err := n.Run("", cmd)
	logrus.Info(string(info))
	return err
}

// Match complete aliases before comments, never substrings or regular expressions.
func hostsCommand(mode, addr, name string) (string, error) {
	if err := ValidateHostname(name); err != nil {
		return "", err
	}
	if mode != "remove" && net.ParseIP(addr) == nil {
		return "", fmt.Errorf("invalid hosts IP address %q", addr)
	}
	script := `BEGIN { found=0 } /^[ \t]*#/ { print; next } { line=$0; count=NF; hit=0; for(i=2;i<=count;i++){if($i ~ /^#/)break;if($i==name)hit=1} if(hit)found=1; if(mode=="add" || !hit)print line } END { if(mode=="replace" || (mode=="add" && !found))print addr "  " name }`
	// Keep the inode and permissions of /etc/hosts (including bind mounts).
	cmd := "tmpfile=$(mktemp) && awk -v name=" + shellEscape(name) + " -v addr=" + shellEscape(addr) + " -v mode=" + shellEscape(mode) + " " + shellEscape(script) + ` /etc/hosts > "$tmpfile" && sudo sh -c 'cat "$1" > /etc/hosts' sh "$tmpfile"; result=$?; rm -f -- "$tmpfile"; exit "$result"`
	return cmd, nil
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (n *node) copyFile(sftp *sftp.Client, srcPath, dstPath string) error {

	dstFile, err := sftp.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	srcFile, err := resources.Files().Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	fi, err := srcFile.Stat()
	if err != nil {
		return err
	}

	size := fi.Size()
	bar := pb.New64(size)
	bar.Output = n.stdout
	bar.Prefix(dstPath).Start()
	defer bar.Finish()

	p := sftpPackets.Get().(*[]byte)
	defer sftpPackets.Put(p)
	for buf := *p; ; {
		cnt, err := srcFile.Read(buf)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if _, err := dstFile.Write(buf[:cnt]); err != nil {
			return err
		}
		bar.Add(cnt)
	}
}

func (n *node) copyDir(sftp *sftp.Client, srcDir, dstDir string) error {

	if err := sftp.MkdirAll(dstDir); err != nil {
		return err
	}

	list, err := fs.ReadDir(resources.Files(), srcDir)
	if err != nil {
		return err
	}

	for _, fi := range list {
		name := fi.Name()
		srcPath := path.Join(srcDir, name)
		if name == "x86_64" || name == "aarch64" {
			if name != n.arch {
				continue
			}
			name = ""
		}
		dstPath := path.Join(dstDir, name)
		if fi.IsDir() {
			if err := n.copyDir(sftp, srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := n.copyFile(sftp, srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *node) Run(cwd string, cmds ...string) ([]byte, error) {
	return n.RunContext(n.ctx, cwd, cmds...)
}
func (n *node) RunContext(parent context.Context, cwd string, cmds ...string) ([]byte, error) {
	return n.runContext(parent, n.commandTimeout, cwd, cmds...)
}
func (n *node) run(timeout time.Duration, cwd string, cmds ...string) ([]byte, error) {
	if timeout <= 0 {
		timeout = n.commandTimeout
	}
	return n.runContext(n.ctx, timeout, cwd, cmds...)
}
func (n *node) runContext(parent context.Context, timeout time.Duration, cwd string, cmds ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client, err := n.client()
	if err != nil {
		return nil, err
	}
	cmd := strings.Join(cmds, " && ")
	if cwd != "" {
		cmd = "cd -- " + shellEscape(cwd) + " && " + cmd
	}
	var b bytes.Buffer
	err = n.bounded(ctx, client, "SSH command", func() error {
		s, err := client.NewSession()
		if err != nil {
			return err
		}
		defer s.Close()
		s.Stdout = &b
		s.Stderr = n.stderr
		return s.Run(cmd)
	})
	return b.Bytes(), err
}

func (n *node) StopService(name string) error {
	info, err := n.Run("", fmt.Sprintf("sudo systemctl stop -- %s", shellEscape(name)))
	logrus.Info(string(info))
	return err
}

func (n *node) StartService(name string) error {
	info, err := n.Run("", fmt.Sprintf("sudo systemctl start -- %s", shellEscape(name)))
	logrus.Info(string(info))
	return err
}

func (n *node) Install(name string, a ...string) error {
	return n.install(name, 0, a...)
}

func (n *node) InstallWithTimeout(name string, timeout time.Duration, a ...string) error {
	return n.install(name, timeout, a...)
}

func (n *node) install(name string, timeout time.Duration, a ...string) error {
	if err := n.Stage(name); err != nil {
		return err
	}
	_, dstDir, err := n.componentPaths(name)
	if err != nil {
		return err
	}

	quoted := make([]string, len(a))
	for i, arg := range a {
		quoted[i] = shellEscape(arg)
	}
	info, err := n.run(timeout, dstDir,
		"chmod +x install.sh",
		fmt.Sprintf("bash install.sh %s", strings.Join(quoted, " ")),
	)
	logrus.Info(string(info))
	return err
}

func (n *node) ReadFile(path string) ([]byte, error) {
	var result []byte
	err := n.transfer(func(client *sftp.Client) error { var err error; result, err = n.readFile(client, path); return err })
	return result, err
}
func (n *node) readFile(sftp *sftp.Client, filePath string) ([]byte, error) {

	if !strings.HasPrefix(filePath, "~") {
		filePath = path.Join("resource", filePath)
	} else {
		filePath = strings.Replace(filePath, "~", n.home, 1)
	}

	file, err := sftp.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %q %s", filePath, err)
	}
	buf, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (n *node) transfer(fn func(*sftp.Client) error) error {
	ctx, cancel := context.WithTimeout(n.ctx, n.transferTimeout)
	defer cancel()
	client, err := n.client()
	if err != nil {
		return err
	}
	return n.bounded(ctx, client, "SSH transfer", func() error {
		s, err := sftp.NewClient(client, sftp.MaxPacket(sftpMaxPacket))
		if err != nil {
			return err
		}
		defer s.Close()
		return fn(s)
	})
}
func (n *node) componentPaths(name string) (string, string, error) {
	if name == "." || !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return "", "", errors.New("invalid component name")
	}
	return name, path.Join("resource", name), nil
}

// Stage uploads the selected component resources, including architecture-specific offline assets.
func (n *node) Stage(name string) error {
	src, dst, err := n.componentPaths(name)
	if err != nil {
		return err
	}
	if _, err := fs.Stat(resources.Files(), src); err != nil {
		return fmt.Errorf("unknown installation component %q: %w", name, err)
	}
	return n.transfer(func(s *sftp.Client) error {
		if err := s.MkdirAll("resource"); err != nil {
			return err
		}
		if err := n.copyFile(s, "common.sh", "resource/common.sh"); err != nil {
			return err
		}
		return n.copyDir(s, src, dst)
	})
}
