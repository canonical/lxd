package drivers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/lxd/shared"
)

// fsExists checks that the Ceph FS instance indeed exists.
func (d *cephfs) fsExists(ctx context.Context, clusterName string, userName string, fsName string) (bool, error) {
	_, err := shared.RunCommandContext(ctx, "ceph", "--name", "client."+userName, "--cluster", clusterName, "fs", "get", fsName)
	if err != nil {
		status, _ := shared.ExitStatus(err)
		// If the error status code is 2, the fs definitely doesn't exist.
		if status == 2 {
			return false, nil
		}

		// Else, the error status is not 0 or 2,
		// so we can't be sure if the fs exists or not
		// as it might be a network issue, an internal ceph issue, etc.
		return false, err
	}

	return true, nil
}

// osdPoolExists checks that the Ceph OSD Pool indeed exists.
func (d *cephfs) osdPoolExists(ctx context.Context, clusterName string, userName string, osdPoolName string) (bool, error) {
	_, err := shared.RunCommandContext(ctx, "ceph", "--name", "client."+userName, "--cluster", clusterName, "osd", "pool", "get", osdPoolName, "size")
	if err != nil {
		status, _ := shared.ExitStatus(err)
		// If the error status code is 2, the pool definitely doesn't exist.
		if status == 2 {
			return false, nil
		}

		// Else, the error status is not 0 or 2,
		// so we can't be sure if the pool exists or not
		// as it might be a network issue, an internal ceph issue, etc.
		return false, err
	}

	return true, nil
}

// getOSDPoolDefaultSize gets the global OSD default pool size that is used for
// all pools created without an explicit OSD pool size.
func (d *cephfs) getOSDPoolDefaultSize() (int, error) {
	size, err := shared.TryRunCommand("ceph",
		"--name", "client."+d.config["cephfs.user.name"],
		"--cluster", d.config["cephfs.cluster_name"],
		"config",
		"get",
		"mon",
		"osd_pool_default_size",
		"--format",
		"json")
	if err != nil {
		return -1, err
	}

	var sizeInt int
	err = json.Unmarshal([]byte(size), &sizeInt)
	if err != nil {
		return -1, err
	}

	return sizeInt, nil
}

// getConfig parses the Ceph configuration file and returns the list of monitors and secret key.
func (d *cephfs) getConfig(clusterName string, userName string) ([]string, string, error) {
	// Get the monitor list.
	monitors, err := CephMonitors(clusterName)
	if err != nil {
		return nil, "", err
	}

	// Get the keyring entry.
	secret, err := CephKeyring(clusterName, userName)
	if err != nil {
		return nil, "", err
	}

	return monitors, secret, nil
}

// getMountOptions returns the common mount options for volumes.
func (d *cephfs) getMountOptions(name string, secret string, namespace string) string {
	// The default mount_timeout is 60s.
	// Setting it to 10s ensures the mount operation doesn't take longer than the default deadline which is used by TryMount.
	return fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v,mount_timeout=10", name, secret, namespace)
}
