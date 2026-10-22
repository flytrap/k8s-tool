package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestStepListingUsesExecutionMode(t *testing.T) {
	for _, update := range []bool{false, true} {
		var out bytes.Buffer
		app := &cli.App{Commands: []*cli.Command{newInstallCmd()}, Writer: &out, ErrWriter: &out}
		args := []string{"k8s-tool", "install", "--config", "/missing/config.yaml", "--steps"}
		if update {
			args = append(args, "--update")
		}
		if err := app.Run(args); err != nil {
			t.Fatal(err)
		}
		if update {
			if !strings.Contains(out.String(), "9 :  join node") || !strings.Contains(out.String(), "10 :  synchronize HA") || strings.Contains(out.String(), "install app") {
				t.Fatal(out.String())
			}
		} else if !strings.Contains(out.String(), "14 :  install app") || strings.Contains(out.String(), "synchronize HA") {
			t.Fatal(out.String())
		}
	}
}
func TestDefaultConfigAndMissingConfigError(t *testing.T) {
	cmd := newInstallCmd()
	var path string
	cmd.Action = func(c *cli.Context) error { path = c.String("config"); return nil }
	if err := (&cli.App{Commands: []*cli.Command{cmd}}).Run([]string{"k8s-tool", "install"}); err != nil {
		t.Fatal(err)
	}
	if path != "config.yaml" {
		t.Fatalf("default config %q", path)
	}
	app := &cli.App{Commands: []*cli.Command{newInstallCmd()}}
	if err := app.Run([]string{"k8s-tool", "install", "--config", "/missing/config.yaml"}); err == nil || !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("expected actionable error: %v", err)
	}
}

func TestSkipHostKeyCheckPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured bool
		args       []string
		want       bool
	}{
		{name: "secure default"},
		{name: "configuration enables skip", configured: true, want: true},
		{name: "flag enables skip", args: []string{"--insecure-skip-host-key-check"}, want: true},
		{name: "explicit false overrides config", configured: true, args: []string{"--insecure-skip-host-key-check=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newInstallCmd()
			cmd.Action = func(ctx *cli.Context) error {
				if got := skipHostKeyCheck(ctx, tc.configured); got != tc.want {
					t.Fatalf("skip=%v, want=%v", got, tc.want)
				}
				return nil
			}
			args := append([]string{"k8s-tool", "install"}, tc.args...)
			if err := (&cli.App{Commands: []*cli.Command{cmd}}).Run(args); err != nil {
				t.Fatal(err)
			}
		})
	}
}
