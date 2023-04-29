//go:build !linux

package main

import (
	"fmt"

	"github.com/lxc/lxd/lxd/metrics"
)

func getFilesystemMetrics(d *Daemon) (map[string]metrics.FilesystemMetrics, error) {
	return nil, fmt.Errorf("Not implemented")
}
