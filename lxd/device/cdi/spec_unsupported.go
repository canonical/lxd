//go:build armhf || arm || arm32

package cdi

import (
	"fmt"

	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
)

func defaultNvidiaTegraCSVFiles(rootPath string) []string {
	return []string{}
}

func generateSpec(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	return nil, fmt.Errorf("NVIDIA CDI operations not supported on this platform")
}
