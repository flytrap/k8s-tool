package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"k8s-tool/app/config"
	"k8s-tool/app/engine"
	"k8s-tool/app/node"

	"github.com/urfave/cli/v2"
)

var VERSION = "0.0.1"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := &cli.App{
		Name:        "k8s-tool",
		Usage:       "automatic install kubernetes and other components",
		Description: "install or expand a Kubernetes cluster over SSH",
		Commands: []*cli.Command{
			newInstallCmd(),
		},
		Version: VERSION,
	}
	if err := app.RunContext(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newInstallCmd() *cli.Command {
	return &cli.Command{
		Name:        "install",
		Description: "read a config file and immediately install",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "insecure-skip-host-key-check",
				Usage: "skip SSH known_hosts and SHA256 fingerprint verification",
			},
			&cli.StringFlag{
				Name:  "config",
				Usage: "path to config file",
				Value: "config.yaml",
			},
			&cli.BoolFlag{
				Name:  "update",
				Usage: "update k8s join new node",
				Value: false,
			},
			&cli.BoolFlag{
				Name:  "steps",
				Usage: "print steps",
				Value: false,
			},
			&cli.StringFlag{
				Name:        "step",
				Usage:       "install step",
				DefaultText: "",
			},
		},
		Action: install,
	}
}

func install(ctx *cli.Context) error {
	if ctx.Bool("steps") {
		printSteps(ctx.App.Writer, ctx.Bool("update"))
		return nil
	}
	c, err := config.Load(ctx.String("config"))
	if err != nil {
		return err
	}

	e, err := engine.New(
		engine.Context(ctx.Context),
		engine.Namespace(c.Namespace),
		engine.KubernetesVersion(c.KubernetesVersion),
		engine.IstioVersion(c.IstioVersion),
		engine.CRISocket(c.CRISocket),
		engine.Registry(c.Registry),
		engine.Vip(c.Vip),
		engine.Region(c.Region),
		engine.NTP(c.NTP.Server, c.NTP.Allow, c.NTP.Timezone),
		engine.NFS(c.NFS.Server, c.NFS.Path),
	)
	if err != nil {
		return err
	}
	for _, nn := range c.Nodes {
		opts := []node.Option{
			node.Context(ctx.Context),
			node.KnownHosts(c.SSH.KnownHosts),
			node.InsecureSkipHostKeyCheck(skipHostKeyCheck(ctx, c.SSH.InsecureSkipHostKeyCheck)),
			node.HostKeyFingerprint(nn.HostKeyFingerprint),
			node.ConnectTimeout(c.SSH.ConnectTimeout),
			node.CommandTimeout(c.SSH.CommandTimeout),
			node.TransferTimeout(c.SSH.TransferTimeout),
			node.Address(nn.Address),
			node.Role(nn.Role),
			node.Port(nn.Port),
			node.Password(nn.Password),
			node.KeyPath(nn.KeyPath),
		}
		if nn.Username != "" {
			opts = append(opts, node.Username(nn.Username))
		}
		n, err := node.New(opts...)
		if err != nil {
			return err
		}
		n.SetHostname(nn.Hostname)
		if err := e.AddNode(n); err != nil {
			return err
		}
	}
	if ctx.Bool("update") {
		return e.Update(ctx.String("step"))
	}
	return e.Install(ctx.String("step"))
}

func skipHostKeyCheck(ctx *cli.Context, configured bool) bool {
	if ctx.IsSet("insecure-skip-host-key-check") {
		return ctx.Bool("insecure-skip-host-key-check")
	}
	return configured
}

func printSteps(w io.Writer, update bool) {
	steps := engine.DeploySteps
	if update {
		steps = engine.UpdateSteps
	}
	for _, i := range steps {
		fmt.Fprintln(w, i.Num, ": ", i.Name)
	}
}
