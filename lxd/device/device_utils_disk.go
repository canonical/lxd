package device

import (
	"bufio"
	"fmt"
	"github.com/lxc/lxd/lxd/db"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared/logger"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// StorageVolumeMount checks if storage volume is mounted and if not tries to mount it.
var StorageVolumeMount func(s *state.State, poolName string, volumeName string, volumeTypeName string, instance Instance) error

// StorageVolumeUmount unmounts a storage volume.
var StorageVolumeUmount func(s *state.State, poolName string, volumeName string, volumeType int) error

// StorageRootFSApplyQuota applies a new quota.
var StorageRootFSApplyQuota func(s *state.State, instance Instance, size string) error

// BlockFsDetect detects the type of block device.
func BlockFsDetect(dev string) (string, error) {
	out, err := shared.RunCommand("blkid", "-s", "TYPE", "-o", "value", dev)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// IsBlockdev returns boolean indicating whether device is block type.
func IsBlockdev(path string) bool {
	// Get a stat struct from the provided path
	stat := unix.Stat_t{}
	err := unix.Stat(path, &stat)
	if err != nil {
		return false
	}

	// Check if it's a block device
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		return true
	}

	// Not a device
	return false
}

// DiskMount mounts a disk device.
func DiskMount(srcPath string, dstPath string, readonly bool, recursive bool, propagation string) error {
	var err error

	// Prepare the mount flags
	flags := 0
	if readonly {
		flags |= unix.MS_RDONLY
	}

	// Detect the filesystem
	fstype := "none"
	if IsBlockdev(srcPath) {
		fstype, err = BlockFsDetect(srcPath)
		if err != nil {
			return err
		}
	} else {
		flags |= unix.MS_BIND
		if propagation != "" {
			switch propagation {
			case "private":
				flags |= unix.MS_PRIVATE
			case "shared":
				flags |= unix.MS_SHARED
			case "slave":
				flags |= unix.MS_SLAVE
			case "unbindable":
				flags |= unix.MS_UNBINDABLE
			case "rprivate":
				flags |= unix.MS_PRIVATE | unix.MS_REC
			case "rshared":
				flags |= unix.MS_SHARED | unix.MS_REC
			case "rslave":
				flags |= unix.MS_SLAVE | unix.MS_REC
			case "runbindable":
				flags |= unix.MS_UNBINDABLE | unix.MS_REC
			default:
				return fmt.Errorf("Invalid propagation mode '%s'", propagation)
			}
		}

		if recursive {
			flags |= unix.MS_REC
		}
	}

	// Mount the filesystem
	err = unix.Mount(srcPath, dstPath, fstype, uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Unable to mount %s at %s: %s", srcPath, dstPath, err)
	}

	// Remount bind mounts in readonly mode if requested
	if readonly == true && flags&unix.MS_BIND == unix.MS_BIND {
		flags = unix.MS_RDONLY | unix.MS_BIND | unix.MS_REMOUNT
		err = unix.Mount("", dstPath, fstype, uintptr(flags), "")
		if err != nil {
			return fmt.Errorf("Unable to mount %s in readonly mode: %s", dstPath, err)
		}
	}

	flags = unix.MS_REC | unix.MS_SLAVE
	err = unix.Mount("", dstPath, "", uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("unable to make mount %s private: %s", dstPath, err)
	}

	return nil
}

func diskCephRbdMap(clusterName string, userName string, poolName string, volumeName string) (string, error) {
	devPath, err := shared.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"map",
		fmt.Sprintf("%s_%s", db.StoragePoolVolumeTypeNameCustom, volumeName))
	if err != nil {
		return "", err
	}

	idx := strings.Index(devPath, "/dev/rbd")
	if idx < 0 {
		return "", fmt.Errorf("Failed to detect mapped device path")
	}

	devPath = devPath[idx:]
	return strings.TrimSpace(devPath), nil
}

func diskCephRbdUnmap(deviceName string) error {
	unmapImageName := fmt.Sprintf("%s_%s", db.StoragePoolVolumeTypeNameCustom, deviceName)
	busyCount := 0
again:
	_, err := shared.RunCommand(
		"rbd",
		"unmap",
		unmapImageName)
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
			if ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				if waitStatus.ExitStatus() == 22 {
					// EINVAL (already unmapped)
					return nil
				}

				if waitStatus.ExitStatus() == 16 {
					// EBUSY (currently in use)
					busyCount++
					if busyCount == 10 {
						return err
					}

					// Wait a second an try again
					time.Sleep(time.Second)
					goto again
				}
			}
		}

		return err
	}
	goto again
}

func cephFsConfig(clusterName string, userName string) ([]string, string, error) {
	// Parse the CEPH configuration
	cephConf, err := os.Open(fmt.Sprintf("/etc/ceph/%s.conf", clusterName))
	if err != nil {
		return nil, "", err
	}

	cephMon := []string{}

	scan := bufio.NewScanner(cephConf)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "mon_host") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			servers := strings.Split(fields[1], ",")
			for _, server := range servers {
				cephMon = append(cephMon, strings.TrimSpace(server))
			}
			break
		}
	}

	if len(cephMon) == 0 {
		return nil, "", fmt.Errorf("Couldn't find a CPEH mon")
	}

	// Parse the CEPH keyring
	cephKeyring, err := os.Open(fmt.Sprintf("/etc/ceph/%v.client.%v.keyring", clusterName, userName))
	if err != nil {
		return nil, "", err
	}

	var cephSecret string

	scan = bufio.NewScanner(cephKeyring)
	for scan.Scan() {
		line := scan.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "key") {
			fields := strings.SplitN(line, "=", 2)
			if len(fields) < 2 {
				continue
			}

			cephSecret = strings.TrimSpace(fields[1])
			break
		}
	}

	if cephSecret == "" {
		return nil, "", fmt.Errorf("Couldn't find a keyring entry")
	}

	return cephMon, cephSecret, nil
}

func diskCephfsMount(clusterName string, userName string, fsName string, path string) error {
	logger.Debugf("Mounting CEPHFS ")
	// Parse the namespace / path
	fields := strings.SplitN(fsName, "/", 2)
	fsName = fields[0]
	fsPath := "/"
	if len(fields) > 1 {
		fsPath = fields[1]
	}

	// Get the credentials and host
	monAddresses, secret, err := cephFsConfig(clusterName, userName)
	if err != nil {
		return err
	}

	// Do the actual mount
	connected := false
	for _, monAddress := range monAddresses {
		uri := fmt.Sprintf("%s:6789:/%s", monAddress, fsPath)
		err = driver.TryMount(uri, path, "ceph", 0, fmt.Sprintf("name=%v,secret=%v,mds_namespace=%v", userName, secret, fsName))
		if err != nil {
			continue
		}

		connected = true
		break
	}

	if !connected {
		return err
	}

	logger.Debugf("Mounted CEPHFS")

	return nil
}
