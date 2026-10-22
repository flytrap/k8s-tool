// Package resources contains versioned installation scripts and offline assets shipped with the CLI.
package resources

import (
	"embed"
	"io/fs"
)

const (
	ContainerdVersion            = "2.3.6"
	RuncVersion                  = "1.4.2"
	CNIVersion                   = "1.8.0"
	CalicoVersion                = "v3.33.0"
	HelmVersion                  = "v3.21.3"
	MetricsServerVersion         = "v0.8.1"
	KubeStateMetricsChartVersion = "8.6.0"
	NFSChartVersion              = "4.0.18"
	DefaultCRISocket             = "unix:///run/containerd/containerd.sock"
)

//go:embed files
var embedded embed.FS

// Files returns the resource tree with component directories at its root.
func Files() fs.FS {
	tree, err := fs.Sub(embedded, "files")
	if err != nil {
		panic(err)
	}
	return tree
}
