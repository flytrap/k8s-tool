package main

import (
	"context"
	"fmt"
	"k8s-tool/app/config"
	"k8s-tool/app/engine"
	"k8s-tool/app/node"
	"os"

	"github.com/urfave/cli/v2"
)

var VERSION = "0.0.1"

func main() {
	app := &cli.App{
		Name:        "k8s-tool",
		Usage:       "automatic install kubernetes and other components",
		Description: "run without subcommands to start the server",
		Commands: []*cli.Command{
			newWebCmd(context.Background()),
		},
		Version: VERSION,
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newWebCmd(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:        "install",
		Description: "read a config file and immediately install",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "config",
				Usage:       "path to config file",
				DefaultText: "config.yml",
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
		printSteps()
		return nil
	}
	config.MustLoad(ctx.String("config"))
	c := config.C

	e, err := engine.New(
		engine.Namespace(c.Namespace),
		engine.CRISocket(c.CRISocket),
		engine.Registry(c.Registry),
		engine.Vip(c.Vip),
		engine.NTP(c.NTP.Server, c.NTP.Allow, c.NTP.Timezone),
		engine.NFS(c.NFS.Server, c.NFS.Path),
	)
	if err != nil {
		return err
	}
	for i := range c.Nodes {
		n, err := node.New(
			node.Address(c.Nodes[i].Address),
			node.Role(c.Nodes[i].Role),
			node.Port(c.Nodes[i].Port),
			node.Username(c.Nodes[i].Username),
			node.Password(c.Nodes[i].Password),
			node.KeyPath(c.Nodes[i].KeyPath),
		)
		n.SetHostname(c.Nodes[i].Hostname)
		if err != nil {
			return err
		}
		if err := e.AddNode(n); err != nil {
			return err
		}
	}
	if ctx.Bool("update") {
		return e.Update(ctx.String("step"))
	}
	return e.Install(ctx.String("step"))
}

func printSteps() {
	for _, i := range engine.DeploySteps {
		fmt.Fprintln(os.Stderr, i.Num, ": ", i.Name)
	}
}
