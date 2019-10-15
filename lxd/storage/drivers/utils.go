package drivers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func wipeDirectory(path string) error {
	// List all entries
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
	}

	// Individually wipe all entries
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		err := os.RemoveAll(entryPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func forceUnmount(path string) (bool, error) {
	unmounted := false

	for {
		// Check if already unmounted
		if !shared.IsMountPoint(path) {
			return unmounted, nil
		}

		// Try a clean unmount first
		err := unix.Unmount(path, 0)
		if err != nil {
			// Fallback to lazy unmounting
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return false, err
			}
		}

		unmounted = true
	}
}

func sameMount(srcPath string, dstPath string) bool {
	// Get the source vfs path information
	var srcFsStat unix.Statfs_t
	err := unix.Statfs(srcPath, &srcFsStat)
	if err != nil {
		return false
	}

	// Get the destination vfs path information
	var dstFsStat unix.Statfs_t
	err = unix.Statfs(dstPath, &dstFsStat)
	if err != nil {
		return false
	}

	// Compare statfs
	if srcFsStat.Type != dstFsStat.Type || srcFsStat.Fsid != dstFsStat.Fsid {
		return false
	}

	// Get the source path information
	var srcStat unix.Stat_t
	err = unix.Stat(srcPath, &srcStat)
	if err != nil {
		return false
	}

	// Get the destination path information
	var dstStat unix.Stat_t
	err = unix.Stat(dstPath, &dstStat)
	if err != nil {
		return false
	}

	// Compare inode
	if srcStat.Ino != dstStat.Ino {
		return false
	}

	return true
}

func tryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	// Attempt 20 mounts over 10s
	for i := 0; i < 20; i++ {
		err = unix.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func vfsResources(path string) (*api.ResourcesStoragePool, error) {
	// Get the VFS information
	st, err := shared.Statvfs(path)
	if err != nil {
		return nil, err
	}

	// Fill in the struct
	res := api.ResourcesStoragePool{}
	res.Space.Total = st.Blocks * uint64(st.Bsize)
	res.Space.Used = (st.Blocks - st.Bfree) * uint64(st.Bsize)

	// Some filesystems don't report inodes since they allocate them
	// dynamically e.g. btrfs.
	if st.Files > 0 {
		res.Inodes.Total = st.Files
		res.Inodes.Used = st.Files - st.Ffree
	}

	return &res, nil
}

// GetPoolMountPoint returns the mountpoint of the given pool.
// {LXD_DIR}/storage-pools/<pool>
func GetPoolMountPoint(poolName string) string {
	return shared.VarPath("storage-pools", poolName)
}

// GetVolumeMountPoint returns the mount path for a specific volume based on its pool and type and
// whether it is a snapshot or not.
// For VolumeTypeImage the volName is the image fingerprint.
func GetVolumeMountPoint(poolName string, volType VolumeType, volName string) string {
	if shared.IsSnapshot(volName) {
		return shared.VarPath("storage-pools", poolName, fmt.Sprintf("%s-snapshots", string(volType)), project.Prefix("default", volName))
	}

	return shared.VarPath("storage-pools", poolName, string(volType), project.Prefix("default", volName))
}

// DeleteParentSnapshotDirIfEmpty removes the parent snapshot directory if it is empty.
// It accepts the volume name of a snapshot in the form "volume/snap" and the volume path of the
// snapshot. It will then remove the snapshots directory above "/snap" if it is empty.
func DeleteParentSnapshotDirIfEmpty(volName string, volPath string) error {
	_, snapName, isSnap := shared.ContainerGetParentAndSnapshotName(volName)
	if !isSnap {
		return fmt.Errorf("Volume is not a snapshot")
	}

	// Extract just the snapshot name from the volume name and then remove that suffix
	// from the volume path. This will get us the parent snapshots directory we need.
	snapshotsPath := strings.TrimSuffix(volPath, snapName)
	isEmpty, err := shared.PathIsEmpty(snapshotsPath)
	if err != nil {
		return err
	}

	if isEmpty {
		err := os.Remove(snapshotsPath)
		if err != nil {
			return err
		}
	}

	return nil
}
