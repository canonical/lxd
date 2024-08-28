//go:build armhf || arm || arm32

package cdi

import (
	"fmt"

	"github.com/canonical/lxd/lxd/instance"
	"tags.cncf.io/container-device-interface/specs-go"
)

func defaultNvidiaTegraCSVFiles(rootPath string) []string {
	return []string{}
}

func generateSpec(cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	return nil, fmt.Errorf("NVIDIA CDI operations not supported on this platform")
}
