package node

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cheggaaa/pb"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
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
		AddHost(addr, name string) error
		RemoveHost(name string) error
		ReplaceHost(addr, name string) error
		Install(name string, a ...string) error
		ReadFile(path string) ([]byte, error)
		StopService(name string) error
		StartService(name string) error
		Run(cwd string, cmds ...string) ([]byte, error)
	}

	node struct {
		base
		stdout io.Writer
		stderr io.Writer
		sshcli *ssh.Client
		os     string
		arch   string
		home   string
	}
)

func New(opts ...Option) (Node, error) {
	n := &node{}
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
	return n, nil
}

func (n *node) Connect() error {
	addr := fmt.Sprintf("%s:%d", n.addr, n.port)
	auth := []ssh.AuthMethod{ssh.Password(n.password)}
	if n.keyPath != "" {
		key, err := os.ReadFile(n.keyPath)
		if err != nil {
			return fmt.Errorf("read key file %q: %w", n.keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return fmt.Errorf("parse private key %q: %w", n.keyPath, err)
		}
		auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            n.username,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return err
	}
	n.sshcli = client

	if err := n.fetchOS(); err != nil {
		return err
	}
	if err := n.fetchArch(); err != nil {
		return err
	}
	return n.fetchHome()
}

func (n *node) Close() {
	if n.sshcli != nil {
		n.sshcli.Close()
	}
}

func (n *node) output(cmds ...string) (string, error) {
	s, err := n.sshcli.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()

	s.Stderr = n.stderr
	buf, err := s.Output(strings.Join(cmds, " && "))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func (n *node) fetchOS() error {
	os, err := n.output("cat /etc/os-release | grep -w ID | awk -F '=' '{print $2}'")
	if err != nil {
		return err
	}

	os = strings.Trim(os, "\"\n")
	switch os {
	case "centos", "ubuntu":
		n.os = os
	case "kylin":
		if _, err := n.output("type apt 2>/dev/null"); err == nil {
			n.os = "ubuntu"
		} else if _, err := n.output("type yum 2>/dev/null"); err == nil {
			n.os = "centos"
		} else {
			return errors.New("kylin used neither ubuntu nor centos")
		}
	default:
		return fmt.Errorf("unsupported operation system %q", os)
	}
	log.Printf("Node %s os: %s\n", n.addr, n.os)
	return nil
}

func (n *node) fetchArch() error {
	arch, err := n.output("arch")
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
	log.Printf("Node %s arch: %s\n", n.addr, n.arch)
	return nil
}

func (n *node) fetchHome() error {
	home, err := n.output("echo -n $HOME")
	if err != nil {
		return err
	}

	n.home = home
	log.Printf("Node %s home: %s\n", n.addr, n.home)
	return nil
}

func (n *node) AddHost(addr, name string) error {
	cmd := fmt.Sprintf("sudo sed -i '$a %s  %s' /etc/hosts", addr, name)
	info, err := n.Run("", cmd)
	logrus.Info(string(info))
	return err
}

func (n *node) RemoveHost(name string) error {
	cmd := fmt.Sprintf("sudo sed -ie '/%s/d' /etc/hosts", name)
	info, err := n.Run("", cmd)
	logrus.Info(string(info))
	return err
}

func (n *node) ReplaceHost(addr, name string) error {
	cmd := fmt.Sprintf("sudo sed -ie '/%s/d' /etc/hosts && "+
		"sudo sed -i '$a %s  %s' /etc/hosts", name, addr, name)
	info, err := n.Run("", cmd)
	logrus.Info(string(info))
	return err
}

func (n *node) copyFile(srcPath, dstPath string) error {
	sftp, err := sftp.NewClient(n.sshcli, sftp.MaxPacket(sftpMaxPacket))
	if err != nil {
		return err
	}
	defer sftp.Close()

	dstFile, err := sftp.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	srcFile, err := os.Open(srcPath)
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

func (n *node) copyDir(srcDir, dstDir string) error {
	sftp, err := sftp.NewClient(n.sshcli, sftp.MaxPacket(sftpMaxPacket))
	if err != nil {
		return err
	}
	defer sftp.Close()

	if err := sftp.MkdirAll(dstDir); err != nil {
		return err
	}

	list, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, fi := range list {
		name := fi.Name()
		srcPath := filepath.Join(srcDir, name)
		if name == "x86_64" || name == "aarch64" {
			if name != n.arch {
				continue
			}
			name = ""
		}
		dstPath := filepath.Join(srcDir, name)
		if fi.IsDir() {
			if err := n.copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := n.copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *node) Run(cwd string, cmds ...string) ([]byte, error) {
	cmd := strings.Join(cmds, " && ")
	if cwd != "" {
		cmd = fmt.Sprintf("cd %s && %s && cd ~", cwd, cmd)
	}

	s, err := n.sshcli.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()

	var b bytes.Buffer
	s.Stdout = &b
	s.Stderr = n.stderr
	if err := s.Run(cmd); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (n *node) StopService(name string) error {
	info, err := n.Run("", fmt.Sprintf("sudo systemctl stop %s", name))
	logrus.Info(string(info))
	return err
}

func (n *node) StartService(name string) error {
	info, err := n.Run("", fmt.Sprintf("sudo systemctl start %s", name))
	logrus.Info(string(info))
	return err
}

func (n *node) Install(name string, a ...string) error {
	srcDir := filepath.Join("resource", name)
	dstDir := filepath.Join("resource", name)

	path := filepath.Join(srcDir, n.os)
	if _, err := os.Stat(path); err == nil {
		srcDir = filepath.Join(srcDir, n.os)
		dstDir = filepath.Join(dstDir, n.os)
	}

	if err := n.copyDir(srcDir, dstDir); err != nil {
		return err
	}

	info, err := n.Run(dstDir,
		fmt.Sprint("chmod", " ", "+x", " ", "install.sh", " "),
		fmt.Sprint("bash", " ", "install.sh", " ", strings.Join(a, " ")),
	)
	logrus.Info(string(info))
	return err
}

func (n *node) ReadFile(path string) ([]byte, error) {
	sftp, err := sftp.NewClient(n.sshcli, sftp.MaxPacket(sftpMaxPacket))
	if err != nil {
		return nil, err
	}
	defer sftp.Close()

	if !strings.HasPrefix(path, "~") {
		path = filepath.Join("resource", path)
	} else {
		path = strings.Replace(path, "~", n.home, 1)
	}

	file, err := sftp.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q %s", path, err)
	}
	buf, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return nil, err
	}
	return buf, nil
}
