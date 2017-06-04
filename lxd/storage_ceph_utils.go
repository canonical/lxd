package main

import (
	"github.com/lxc/lxd/shared"
)

// cephOSDPoolExists checks whether a given OSD pool exists.
func cephOSDPoolExists(ClusterName string, poolName string) bool {
	_, err := shared.RunCommand(
		"ceph",
		"--cluster", ClusterName,
		"osd",
		"pool",
		"get",
		poolName,
		"size")
	if err != nil {
		return false
	}

	return true
}

// cephOSDPoolDestroy destroys an OSD pool.
// - A call to cephOSDPoolDestroy will destroy a pool including any storage
//   volumes that still exist in the pool.
// - In case the OSD pool that is supposed to be deleted does not exist this
//   command will still exit 0. This means that if the caller wants to be sure
//   that this call actually deleted an OSD pool it needs to check for the
//   existence of the pool first.
func cephOSDPoolDestroy(clusterName string, poolName string) error {
	_, err := shared.RunCommand("ceph",
		"--cluster", clusterName,
		"osd",
		"pool",
		"delete",
		poolName,
		"--yes-i-really-really-mean-it")
	if err != nil {
		return err
	}

	return nil
}
