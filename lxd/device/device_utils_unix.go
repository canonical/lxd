package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

// instanceUnixGetDeviceAttributes returns the UNIX device attributes for an instance device.
// Uses supplied device config for device properties, and if they haven't been set, falls back to
// using UnixGetDeviceAttributes() to directly query an existing device file.
func instanceUnixGetDeviceAttributes(devicesPath string, prefix string, config config.Device) (string, int, int, error) {
	// Check if we've been passed major and minor numbers already.
	var tmp int
	var err error
	dMajor := -1
	if config["major"] != "" {
		tmp, err = strconv.Atoi(config["major"])
		if err == nil {
			dMajor = tmp
		}
	}

	dMinor := -1
	if config["minor"] != "" {
		tmp, err = strconv.Atoi(config["minor"])
		if err == nil {
			dMinor = tmp
		}
	}

	dType := ""
	if config["type"] == "unix-char" {
		dType = "c"
	} else if config["type"] == "unix-block" {
		dType = "b"
	}

	// Figure out the paths
	destPath := config["path"]
	if destPath == "" {
		destPath = config["source"]
	}
	relativeDestPath := strings.TrimPrefix(destPath, "/")
	devName := fmt.Sprintf("%s.%s", strings.Replace(prefix, "/", "-", -1), strings.Replace(relativeDestPath, "/", "-", -1))
	devPath := filepath.Join(devicesPath, devName)

	if dType == "" || dMajor < 0 || dMinor < 0 {
		dType, dMajor, dMinor, err = UnixGetDeviceAttributes(devPath)
		if err != nil {
			return dType, dMajor, dMinor, err
		}
	}

	return dType, dMajor, dMinor, err
}

// UnixGetDeviceAttributes returns the decice type, major and minor numbers for a device.
func UnixGetDeviceAttributes(path string) (string, int, int, error) {
	// Get a stat struct from the provided path
	stat := unix.Stat_t{}
	err := unix.Stat(path, &stat)
	if err != nil {
		return "", 0, 0, err
	}

	// Check what kind of file it is
	dType := ""
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		dType = "b"
	} else if stat.Mode&unix.S_IFMT == unix.S_IFCHR {
		dType = "c"
	} else {
		return "", 0, 0, fmt.Errorf("Not a device")
	}

	// Return the device information
	major := shared.Major(stat.Rdev)
	minor := shared.Minor(stat.Rdev)
	return dType, major, minor, nil
}

func unixGetDeviceModeOct(strmode string) (int, error) {
	// Default mode
	if strmode == "" {
		return 0600, nil
	}

	// Converted mode
	i, err := strconv.ParseInt(strmode, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("Bad device mode: %s", strmode)
	}

	return int(i), nil
}

// UnixCreateDevice Unix devices handling.
func UnixCreateDevice(s *state.State, idmapSet *idmap.IdmapSet, devicesPath string, prefix string, m config.Device, defaultMode bool) ([]string, error) {
	var err error
	var major, minor int

	// Extra checks for nesting
	if s.OS.RunningInUserNS {
		for key, value := range m {
			if shared.StringInSlice(key, []string{"major", "minor", "mode", "uid", "gid"}) && value != "" {
				return nil, fmt.Errorf("The \"%s\" property may not be set when adding a device to a nested container", key)
			}
		}
	}

	srcPath := m["source"]
	if srcPath == "" {
		srcPath = m["path"]
	}
	srcPath = shared.HostPath(srcPath)

	// Get the major/minor of the device we want to create
	if m["major"] == "" && m["minor"] == "" {
		// If no major and minor are set, use those from the device on the host
		_, major, minor, err = UnixGetDeviceAttributes(srcPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to get device attributes for %s: %s", m["path"], err)
		}
	} else if m["major"] == "" || m["minor"] == "" {
		return nil, fmt.Errorf("Both major and minor must be supplied for device: %s", m["path"])
	} else {
		major, err = strconv.Atoi(m["major"])
		if err != nil {
			return nil, fmt.Errorf("Bad major %s in device %s", m["major"], m["path"])
		}

		minor, err = strconv.Atoi(m["minor"])
		if err != nil {
			return nil, fmt.Errorf("Bad minor %s in device %s", m["minor"], m["path"])
		}
	}

	// Get the device mode
	mode := os.FileMode(0660)
	if m["mode"] != "" {
		tmp, err := unixGetDeviceModeOct(m["mode"])
		if err != nil {
			return nil, fmt.Errorf("Bad mode %s in device %s", m["mode"], m["path"])
		}
		mode = os.FileMode(tmp)
	} else if !defaultMode {
		mode, err = shared.GetPathMode(srcPath)
		if err != nil {
			errno, isErrno := shared.GetErrno(err)
			if !isErrno || errno != unix.ENOENT {
				return nil, fmt.Errorf("Failed to retrieve mode of device %s: %s", m["path"], err)
			}
			mode = os.FileMode(0660)
		}
	}

	if m["type"] == "unix-block" {
		mode |= unix.S_IFBLK
	} else {
		mode |= unix.S_IFCHR
	}

	// Get the device owner
	uid := 0
	gid := 0

	if m["uid"] != "" {
		uid, err = strconv.Atoi(m["uid"])
		if err != nil {
			return nil, fmt.Errorf("Invalid uid %s in device %s", m["uid"], m["path"])
		}
	}

	if m["gid"] != "" {
		gid, err = strconv.Atoi(m["gid"])
		if err != nil {
			return nil, fmt.Errorf("Invalid gid %s in device %s", m["gid"], m["path"])
		}
	}

	// Create the devices directory if missing
	if !shared.PathExists(devicesPath) {
		os.Mkdir(devicesPath, 0711)
		if err != nil {
			return nil, fmt.Errorf("Failed to create devices path: %s", err)
		}
	}

	destPath := m["path"]
	if destPath == "" {
		destPath = m["source"]
	}
	relativeDestPath := strings.TrimPrefix(destPath, "/")
	devName := fmt.Sprintf("%s.%s", strings.Replace(prefix, "/", "-", -1), strings.Replace(relativeDestPath, "/", "-", -1))
	devPath := filepath.Join(devicesPath, devName)

	// Create the new entry
	if !s.OS.RunningInUserNS {
		encodedDeviceNumber := (minor & 0xff) | (major << 8) | ((minor & ^0xff) << 12)
		err := unix.Mknod(devPath, uint32(mode), encodedDeviceNumber)
		if err != nil {
			return nil, fmt.Errorf("Failed to create device %s for %s: %s", devPath, m["path"], err)
		}

		err = os.Chown(devPath, uid, gid)
		if err != nil {
			return nil, fmt.Errorf("Failed to chown device %s: %s", devPath, err)
		}

		// Needed as mknod respects the umask
		err = os.Chmod(devPath, mode)
		if err != nil {
			return nil, fmt.Errorf("Failed to chmod device %s: %s", devPath, err)
		}

		if idmapSet != nil {
			err := idmapSet.ShiftFile(devPath)
			if err != nil {
				// uidshift failing is weird, but not a big problem.  Log and proceed
				logger.Debugf("Failed to uidshift device %s: %s\n", m["path"], err)
			}
		}
	} else {
		f, err := os.Create(devPath)
		if err != nil {
			return nil, err
		}
		f.Close()

		err = DiskMount(srcPath, devPath, false, false, "")
		if err != nil {
			return nil, err
		}
	}

	return []string{devPath, relativeDestPath}, nil
}
