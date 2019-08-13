package storage

import "github.com/lxc/lxd/shared"

// ContainerPath returns the directory of a container or snapshot.
func ContainerPath(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}
