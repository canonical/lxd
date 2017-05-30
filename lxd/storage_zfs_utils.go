package main

import (
	"strings"

	"github.com/lxc/lxd/shared"
)

// zfsPoolVolumeCreate creates a ZFS dataset with a set of given properties.
func zfsPoolVolumeCreate(dataset string, properties ...string) (string, error) {
	p := strings.Join(properties, ",")
	return shared.RunCommand("zfs", "create", "-o", p, "-p", dataset)
}
