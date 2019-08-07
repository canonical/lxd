package device

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// SCIB Infiniband sys class path.
const SCIB = "/sys/class/infiniband"

// SCNET Net sys class path.
const SCNET = "/sys/class/net"

// IBDevPrefix Infiniband devices prefix.
const IBDevPrefix = "infiniband.unix"

// IBF structure representing Infiniband function config.
type IBF struct {
	// port the function belongs to.
	Port int64

	// name of the {physical,virtual} function.
	Fun string

	// whether this is a physical (true) or virtual (false) function.
	PF bool

	// device of the function.
	Device string

	// uverb device of the function.
	PerPortDevices []string
	PerFunDevices  []string
}

// infinibandLoadDevices inspects the system and returns information about all infiniband devices.
func infinibandLoadDevices() (map[string]IBF, error) {
	// check if there are any infiniband devices.
	fscib, err := os.Open(SCIB)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscib.Close()

	// eg.g. mlx_i for i = 0, 1, ..., n
	IBDevNames, err := fscib.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(IBDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	// Retrieve all network device names.
	fscnet, err := os.Open(SCNET)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscnet.Close()

	// Retrieve all network devices.
	NetDevNames, err := fscnet.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(NetDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	UseableDevices := make(map[string]IBF)
	for _, IBDevName := range IBDevNames {
		IBDevResourceFile := fmt.Sprintf("/sys/class/infiniband/%s/device/resource", IBDevName)
		IBDevResourceBuf, err := ioutil.ReadFile(IBDevResourceFile)
		if err != nil {
			return nil, err
		}

		for _, NetDevName := range NetDevNames {
			NetDevResourceFile := fmt.Sprintf("/sys/class/net/%s/device/resource", NetDevName)
			NetDevResourceBuf, err := ioutil.ReadFile(NetDevResourceFile)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			// If the device and the VF have the same address space
			// they belong together.
			if bytes.Compare(IBDevResourceBuf, NetDevResourceBuf) != 0 {
				continue
			}

			// Now let's find the ports.
			IBDevID := fmt.Sprintf("/sys/class/net/%s/dev_id", NetDevName)
			IBDevPort := fmt.Sprintf("/sys/class/net/%s/dev_port", NetDevName)
			DevIDBuf, err := ioutil.ReadFile(IBDevID)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			DevIDString := strings.TrimSpace(string(DevIDBuf))
			DevIDPort, err := strconv.ParseInt(DevIDString, 0, 64)
			if err != nil {
				return nil, err
			}

			DevPort := int64(0)
			DevPortBuf, err := ioutil.ReadFile(IBDevPort)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				DevPortString := strings.TrimSpace(string(DevPortBuf))
				DevPort, err = strconv.ParseInt(DevPortString, 0, 64)
				if err != nil {
					return nil, err
				}
			}

			Port := DevIDPort
			if DevPort > DevIDPort {
				Port = DevPort
			}
			Port++

			NewIBF := IBF{
				Port:   Port,
				Fun:    IBDevName,
				Device: NetDevName,
			}

			// Identify the /dev/infiniband/uverb<idx> device.
			tmp := []string{}
			IBUverb := fmt.Sprintf("/sys/class/net/%s/device/infiniband_verbs", NetDevName)
			fuverb, err := os.Open(IBUverb)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fuverb.Close()

				// Optional: retrieve all network devices.
				tmp, err = fuverb.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, v)
			}

			// Identify the /dev/infiniband/ucm<idx> device.
			tmp = []string{}
			IBcm := fmt.Sprintf("/sys/class/net/%s/device/infiniband_ucm", NetDevName)
			fcm, err := os.Open(IBcm)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fcm.Close()

				// Optional: retrieve all network devices.
				tmp, err = fcm.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				devPath := fmt.Sprintf("/dev/infiniband/%s", v)
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, devPath)
			}

			// Identify the /dev/infiniband/{issm,umad}<idx> devices.
			IBmad := fmt.Sprintf("/sys/class/net/%s/device/infiniband_mad", NetDevName)
			ents, err := ioutil.ReadDir(IBmad)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				for _, ent := range ents {
					IBmadPort := fmt.Sprintf("%s/%s/port", IBmad, ent.Name())
					portBuf, err := ioutil.ReadFile(IBmadPort)
					if err != nil {
						if !os.IsNotExist(err) {
							return nil, err
						}
						continue
					}

					portStr := strings.TrimSpace(string(portBuf))
					PortMad, err := strconv.ParseInt(portStr, 0, 64)
					if err != nil {
						return nil, err
					}

					if PortMad != NewIBF.Port {
						continue
					}

					if strings.Index(ent.Name(), "-") != -1 {
						return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", ent.Name())
					}

					NewIBF.PerFunDevices = append(NewIBF.PerFunDevices, ent.Name())
				}
			}

			// Figure out whether this is a physical function.
			IBPF := fmt.Sprintf("/sys/class/net/%s/device/physfn", NetDevName)
			NewIBF.PF = !shared.PathExists(IBPF)

			UseableDevices[NetDevName] = NewIBF
		}
	}

	return UseableDevices, nil
}

// infinibandAddDevices creates the UNIX devices for the provided IBF device and then configures the
// supplied runConfig with the Cgroup rules and mount instructions to pass the device into instance.
func infinibandAddDevices(s *state.State, devicesPath string, deviceName string, ifDev *IBF, runConf *RunConfig) error {
	err := infinibandAddDevicesPerPort(s, devicesPath, deviceName, ifDev, runConf)
	if err != nil {
		return err
	}

	err = infinibandAddDevicesPerFun(s, devicesPath, deviceName, ifDev, runConf)
	if err != nil {
		return err
	}

	return nil
}

func infinibandAddDevicesPerPort(s *state.State, devicesPath string, deviceName string, ifDev *IBF, runConf *RunConfig) error {
	for _, unixCharDev := range ifDev.PerPortDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		relDestPath := destPath[1:]
		devPrefix := fmt.Sprintf("%s.%s", IBDevPrefix, deviceName)

		// Unix device.
		dummyDevice := config.Device{
			"source": destPath,
		}

		deviceExists := false

		// Only handle "infiniband.unix." devices.
		prefix := fmt.Sprintf("%s.", IBDevPrefix)

		// Load all devices.
		dents, err := ioutil.ReadDir(devicesPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}

		for _, ent := range dents {
			// Skip non "infiniband.unix." devices.
			devName := ent.Name()
			if !strings.HasPrefix(devName, prefix) {
				continue
			}

			// Extract the path inside the container.
			idx := strings.LastIndex(devName, ".")
			if idx == -1 {
				return fmt.Errorf("Invalid infiniband device name \"%s\"", devName)
			}
			rPath := devName[idx+1:]
			rPath = strings.Replace(rPath, "-", "/", -1)
			if rPath != relDestPath {
				continue
			}

			deviceExists = true
			break
		}

		paths, err := UnixCreateDevice(s, nil, devicesPath, devPrefix, dummyDevice, false)
		if err != nil {
			return err
		}
		devPath := paths[0]

		// If an existing device with the same path in the instance exists, then do not
		// request it be mounted again.
		if deviceExists {
			continue
		}

		// Instruct liblxc to perform the mount.
		runConf.Mounts = append(runConf.Mounts, MountEntryItem{
			DevPath:    devPath,
			TargetPath: relDestPath,
			FSType:     "none",
			Opts:       []string{"bind", "create=file"},
		})

		// Add the new device cgroup rule.
		dType, dMajor, dMinor, err := UnixGetDeviceAttributes(devPath)
		if err != nil {
			return err
		}

		// Instruct liblxc to setup the cgroup rule.
		runConf.CGroups = append(runConf.CGroups, RunConfigItem{
			Key:   "devices.allow",
			Value: fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor),
		})
	}

	return nil
}

func infinibandAddDevicesPerFun(s *state.State, devicesPath string, deviceName string, ifDev *IBF, runConf *RunConfig) error {
	for _, unixCharDev := range ifDev.PerFunDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		uniqueDevPrefix := fmt.Sprintf("%s.%s", IBDevPrefix, deviceName)
		relativeDestPath := fmt.Sprintf("dev/infiniband/%s", unixCharDev)
		uniqueDevName := fmt.Sprintf("%s.%s", uniqueDevPrefix, strings.Replace(relativeDestPath, "/", "-", -1))
		hostDevPath := filepath.Join(devicesPath, uniqueDevName)

		dummyDevice := config.Device{
			"source": destPath,
		}

		// Instruct liblxc to perform the mount.
		runConf.Mounts = append(runConf.Mounts, MountEntryItem{
			DevPath:    hostDevPath,
			TargetPath: relativeDestPath,
			FSType:     "none",
			Opts:       []string{"bind", "create=file"},
		})

		paths, err := UnixCreateDevice(s, nil, devicesPath, uniqueDevPrefix, dummyDevice, false)
		if err != nil {
			return err
		}
		devPath := paths[0]

		// Add the new device cgroup rule.
		dType, dMajor, dMinor, err := UnixGetDeviceAttributes(devPath)
		if err != nil {
			return err
		}

		// Instruct liblxc to setup the cgroup rule.
		runConf.CGroups = append(runConf.CGroups, RunConfigItem{
			Key:   "devices.allow",
			Value: fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor),
		})
	}

	return nil
}

// infinibandRemoveDevices identifies all UNIX devices related to the supplied deviceName and then
// populates the supplied runConf with the instructions to remove cgroup rules and unmount devices.
func infinibandRemoveDevices(devicesPath string, deviceName string, runConf *RunConfig) error {
	// Load all devices.
	dents, err := ioutil.ReadDir(devicesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	prefix := fmt.Sprintf("%s.", IBDevPrefix)
	ourPrefix := fmt.Sprintf("%s.%s", IBDevPrefix, deviceName)
	ourIFBDevs := []string{}
	otherIFBDevs := []string{}

	for _, ent := range dents {
		// Skip non "infiniband.unix." devices.
		devName := ent.Name()
		if !strings.HasPrefix(devName, prefix) {
			continue
		}

		// This is our infiniband device.
		if strings.HasPrefix(devName, ourPrefix) {
			ourIFBDevs = append(ourIFBDevs, devName)
			continue
		}

		// This someone else's infiniband device.
		otherIFBDevs = append(otherIFBDevs, devName)
	}

	// With infiniband it is possible for multiple LXD configured devices to share the same
	// UNIX character device for memory access, as char devices might be per port not per device.
	// Because of this, before we setup instructions to umount the device from the instance we
	// need to check whether any other LXD devices also use the same char device inside the
	// instance. To do this, scan all devices for this instance and use the dev path encoded
	// in the host-side filename to check if any match.
	residualInfinibandDevs := []string{}
	for _, otherIFBDev := range otherIFBDevs {
		idx := strings.LastIndex(otherIFBDev, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid infiniband device name \"%s\"", otherIFBDev)
		}
		// Remove the LXD device name prefix, so we're left with dev path inside the instance.
		relPeerPath := otherIFBDev[idx+1:]
		relPeerPath = strings.Replace(relPeerPath, "-", "/", -1)
		absPeerPath := fmt.Sprintf("/%s", relPeerPath)
		residualInfinibandDevs = append(residualInfinibandDevs, absPeerPath)
	}

	// Check that none of our infiniband devices are in use by another LXD device.
	for _, ourDev := range ourIFBDevs {
		idx := strings.LastIndex(ourDev, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid infiniband device name \"%s\"", ourDev)
		}
		rPath := ourDev[idx+1:]
		rPath = strings.Replace(rPath, "-", "/", -1)
		absPath := fmt.Sprintf("/%s", rPath)
		dupe := false

		// Look for infiniband devices for other LXD devices that match the same path.
		for _, peerDevPath := range residualInfinibandDevs {
			if peerDevPath == absPath {
				dupe = true
				break
			}
		}

		// If a device has been found that points to the same device inside the instance
		// then we cannot request it be umounted inside the instance as it's still in use.
		if dupe {
			continue
		}

		// Append this device to the mount rules (these will be unmounted).
		runConf.Mounts = append(runConf.Mounts, MountEntryItem{
			TargetPath: rPath,
		})

		dummyDevice := config.Device{
			"source": absPath,
		}

		dType, dMajor, dMinor, err := instanceUnixGetDeviceAttributes(devicesPath, ourPrefix, dummyDevice)
		if err != nil {
			return err
		}

		// Append a deny cgroup fule for this device.
		runConf.CGroups = append(runConf.CGroups, RunConfigItem{
			Key:   "devices.deny",
			Value: fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor),
		})
	}

	return nil
}

func infinibandDeleteHostFiles(s *state.State, devicesPath string, deviceName string) error {
	// Load all devices.
	dents, err := ioutil.ReadDir(devicesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Remove our host side device files.
	ourPrefix := fmt.Sprintf("%s.%s", IBDevPrefix, deviceName)
	for _, ent := range dents {
		devName := ent.Name()
		devPath := filepath.Join(devicesPath, devName)

		// Check this is our infiniband device.
		if strings.HasPrefix(devName, ourPrefix) {
			// Remove the host side mount.
			if s.OS.RunningInUserNS {
				unix.Unmount(devPath, unix.MNT_DETACH)
			}

			// Remove the host side device file.
			err = os.Remove(devPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
