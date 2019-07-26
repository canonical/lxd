package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

func (c *containerLXC) fillInfinibandSriovNetworkDevice(name string, m config.Device, reservedDevices map[string]struct{}) (config.Device, error) {
	if m["nictype"] != "sriov" {
		return m, nil
	}

	if m["parent"] == "" {
		return nil, fmt.Errorf("Missing parent for 'sriov' nic '%s'", name)
	}

	newDevice := config.Device{}
	err := shared.DeepCopy(&m, &newDevice)
	if err != nil {
		return nil, err
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", m["parent"])) {
		return nil, fmt.Errorf("Parent device '%s' doesn't exist", m["parent"])
	}
	sriovNumVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", m["parent"])
	sriovTotalVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_totalvfs", m["parent"])

	// verify that this is indeed a SR-IOV enabled device
	if !shared.PathExists(sriovTotalVFs) {
		return nil, fmt.Errorf("Parent device '%s' doesn't support SR-IOV", m["parent"])
	}

	// Get parent dev_port and dev_id values.
	pfDevPort, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_port", m["parent"]))
	if err != nil {
		return nil, err
	}

	pfDevID, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_id", m["parent"]))
	if err != nil {
		return nil, err
	}

	// get number of currently enabled VFs
	sriovNumVfsBuf, err := ioutil.ReadFile(sriovNumVFs)
	if err != nil {
		return nil, err
	}
	sriovNumVfsStr := strings.TrimSpace(string(sriovNumVfsBuf))
	sriovNum, err := strconv.Atoi(sriovNumVfsStr)
	if err != nil {
		return nil, err
	}

	// Check if any VFs are already enabled
	nicName := ""
	for i := 0; i < sriovNum; i++ {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", m["parent"], i)) {
			continue
		}

		// Check if VF is already in use
		empty, err := shared.PathIsEmpty(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", m["parent"], i))
		if err != nil {
			return nil, err
		}
		if empty {
			continue
		}

		vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", m["parent"], i)
		nicName, err = device.NetworkSRIOVGetFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
		if err != nil {
			return nil, err
		}

		// Found a free VF.
		if nicName != "" {
			break
		}
	}

	if nicName == "" {
		return nil, fmt.Errorf("All virtual functions on device \"%s\" are already in use", name)
	}

	newDevice["host_name"] = nicName
	configKey := fmt.Sprintf("volatile.%s.host_name", name)
	c.localConfig[configKey] = nicName

	return newDevice, nil
}

func (c *containerLXC) getInfinibandReserved(m config.Device) (map[string]struct{}, error) {
	instances, err := device.InstanceLoadNodeAll(c.state)
	if err != nil {
		return nil, err
	}

	// Build a unique set of reserved network devices we cannot use.
	reservedDevices := map[string]struct{}{}
	for _, instance := range instances {
		devices := instance.ExpandedDevices()
		config := instance.ExpandedConfig()
		for devName, devConfig := range devices {
			// Record all parent devices, as these are not eligible for use as VFs.
			parent := devConfig["parent"]
			reservedDevices[parent] = struct{}{}

			// If the device has the same parent as us, and a non-empty host_name, then
			// mark that host_name as reserved, as that device is using it.
			if devConfig["type"] == "infiniband" && parent == m["parent"] {
				hostName := config[fmt.Sprintf("volatile.%s.host_name", devName)]
				if hostName != "" {
					reservedDevices[hostName] = struct{}{}
				}
			}
		}
	}

	return reservedDevices, nil
}

func (c *containerLXC) startInfiniband(networkidx int, deviceName string, m config.Device) error {
	infiniband, err := deviceLoadInfiniband()
	if err != nil {
		return err
	}

	reservedDevices, err := c.getInfinibandReserved(m)
	if err != nil {
		return err
	}

	m, err = c.fillInfinibandSriovNetworkDevice(deviceName, m, reservedDevices)
	if err != nil {
		return err
	}

	err = c.initLXCInfiniband(networkidx, m)
	if err != nil {
		return err
	}

	key := m["parent"]
	if m["nictype"] == "sriov" {
		key = m["host_name"]
	}

	ifDev, ok := infiniband[key]
	if !ok {
		return fmt.Errorf("Specified infiniband device \"%s\" not found", key)
	}

	err = c.addInfinibandDevices(deviceName, &ifDev, false)
	if err != nil {
		return err
	}

	// Important we save this to DB now so other devices starting next can see we reserved this
	// host_name device.
	configKey := fmt.Sprintf("volatile.%s.host_name", deviceName)
	err = c.VolatileSet(map[string]string{configKey: key})
	if err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) initLXCInfiniband(networkidx int, m config.Device) error {
	networkKeyPrefix := "lxc.net"
	if !util.RuntimeLiblxcVersionAtLeast(2, 1, 0) {
		networkKeyPrefix = "lxc.network"
	}

	if m["nictype"] == "physical" || m["nictype"] == "sriov" {
		err := lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.type", networkKeyPrefix, networkidx), "phys")
		if err != nil {
			return err
		}
	}

	err := lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.flags", networkKeyPrefix, networkidx), "up")
	if err != nil {
		return err
	}

	if m["nictype"] == "physical" {
		err = lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.link", networkKeyPrefix, networkidx), device.NetworkGetHostDevice(m["parent"], m["vlan"]))
		if err != nil {
			return err
		}
	} else if m["nictype"] == "sriov" {
		err = lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.link", networkKeyPrefix, networkidx), m["host_name"])
		if err != nil {
			return err
		}
	}

	// MAC address
	if m["hwaddr"] != "" {
		err = lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.hwaddr", networkKeyPrefix, networkidx), m["hwaddr"])
		if err != nil {
			return err
		}
	}

	// MTU
	if m["mtu"] != "" {
		err = lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.mtu", networkKeyPrefix, networkidx), m["mtu"])
		if err != nil {
			return err
		}
	}

	// Name
	if m["name"] != "" {
		err = lxcSetConfigItem(c.c, fmt.Sprintf("%s.%d.name", networkKeyPrefix, networkidx), m["name"])
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *containerLXC) addInfinibandDevicesPerPort(deviceName string, ifDev *IBF, devices []os.FileInfo, inject bool) error {
	for _, unixCharDev := range ifDev.PerPortDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		relDestPath := destPath[1:]
		devPrefix := fmt.Sprintf("infiniband.unix.%s", deviceName)

		// Unix device
		dummyDevice := config.Device{
			"source": destPath,
		}

		deviceExists := false
		// only handle infiniband.unix.<device-name>.
		prefix := fmt.Sprintf("infiniband.unix.")
		for _, ent := range devices {

			// skip non infiniband.unix.<device-name> devices
			devName := ent.Name()
			if !strings.HasPrefix(devName, prefix) {
				continue
			}

			// extract the path inside the container
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

		if inject && !deviceExists {
			err := c.insertUnixDevice(devPrefix, dummyDevice, false)
			if err != nil {
				return err
			}
			continue
		}

		paths, err := c.createUnixDevice(devPrefix, dummyDevice, false)
		if err != nil {
			return err
		}
		devPath := paths[0]

		if deviceExists {
			continue
		}

		// inform liblxc about the mount
		err = lxcSetConfigItem(c.c, "lxc.mount.entry",
			fmt.Sprintf("%s %s none bind,create=file 0 0",
				shared.EscapePathFstab(devPath),
				shared.EscapePathFstab(relDestPath)))
		if err != nil {
			return err
		}

		if c.isCurrentlyPrivileged() && !c.state.OS.RunningInUserNS && c.state.OS.CGroupDevicesController {
			// Add the new device cgroup rule
			dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
			if err != nil {
				return err
			}

			err = lxcSetConfigItem(c.c, "lxc.cgroup.devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
			if err != nil {
				return fmt.Errorf("Failed to add cgroup rule for device")
			}
		}
	}

	return nil
}

func (c *containerLXC) addInfinibandDevicesPerFun(deviceName string, ifDev *IBF, inject bool) error {
	for _, unixCharDev := range ifDev.PerFunDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		uniqueDevPrefix := fmt.Sprintf("infiniband.unix.%s", deviceName)
		relativeDestPath := fmt.Sprintf("dev/infiniband/%s", unixCharDev)
		uniqueDevName := fmt.Sprintf("%s.%s", uniqueDevPrefix, strings.Replace(relativeDestPath, "/", "-", -1))
		hostDevPath := filepath.Join(c.DevicesPath(), uniqueDevName)

		dummyDevice := config.Device{
			"source": destPath,
		}

		if inject {
			err := c.insertUnixDevice(uniqueDevPrefix, dummyDevice, false)
			if err != nil {
				return err
			}
			continue
		}

		// inform liblxc about the mount
		err := lxcSetConfigItem(c.c, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,create=file 0 0", hostDevPath, relativeDestPath))
		if err != nil {
			return err
		}

		paths, err := c.createUnixDevice(uniqueDevPrefix, dummyDevice, false)
		if err != nil {
			return err
		}
		devPath := paths[0]
		if c.isCurrentlyPrivileged() && !c.state.OS.RunningInUserNS && c.state.OS.CGroupDevicesController {
			// Add the new device cgroup rule
			dType, dMajor, dMinor, err := deviceGetAttributes(devPath)
			if err != nil {
				return err
			}

			err = lxcSetConfigItem(c.c, "lxc.cgroup.devices.allow", fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor))
			if err != nil {
				return fmt.Errorf("Failed to add cgroup rule for device")
			}
		}
	}

	return nil
}

func (c *containerLXC) addInfinibandDevices(deviceName string, ifDev *IBF, inject bool) error {
	// load all devices
	dents, err := ioutil.ReadDir(c.DevicesPath())
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	err = c.addInfinibandDevicesPerPort(deviceName, ifDev, dents, inject)
	if err != nil {
		return err
	}

	return c.addInfinibandDevicesPerFun(deviceName, ifDev, inject)
}

func (c *containerLXC) addInfinibandDevice(deviceName string, device config.Device) error {
	device, err := c.fillNetworkDevice(deviceName, device)
	if err != nil {
		return err
	}

	var infiniband map[string]IBF
	if device["type"] == "infiniband" {
		infiniband, err = deviceLoadInfiniband()
		if err != nil {
			return err
		}
	}

	reservedDevices, err := c.getInfinibandReserved(device)
	if err != nil {
		return err
	}

	device, err = c.fillInfinibandSriovNetworkDevice(deviceName, device, reservedDevices)
	if err != nil {
		return err
	}

	key := device["parent"]
	if device["nictype"] == "sriov" {
		key = device["host_name"]
	}

	ifDev, ok := infiniband[key]
	if !ok {
		return fmt.Errorf("Specified infiniband device \"%s\" not found", key)
	}

	err = c.addInfinibandDevices(deviceName, &ifDev, true)
	if err != nil {
		return err
	}

	// Add the interface to the container.
	err = c.c.AttachInterface(key, device["name"])
	if err != nil {
		return fmt.Errorf("Failed to attach interface: %s to %s: %s", key, device["name"], err)
	}

	// Important we save this to DB now so other devices starting next can see we reserved this
	// host_name device.
	configKey := fmt.Sprintf("volatile.%s.host_name", deviceName)
	err = c.VolatileSet(map[string]string{configKey: key})
	if err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) removeInfinibandDevice(deviceName string, device config.Device) error {
	device, err := c.fillNetworkDevice(deviceName, device)
	if err != nil {
		return err
	}

	// load all devices
	dents, err := ioutil.ReadDir(c.DevicesPath())
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	tmp := []string{}
	ourInfinibandDevs := []string{}
	prefix := fmt.Sprintf("infiniband.unix.")
	ourPrefix := fmt.Sprintf("infiniband.unix.%s.", deviceName)
	for _, ent := range dents {
		// skip non infiniband.unix.<device-name> devices
		devName := ent.Name()
		if !strings.HasPrefix(devName, prefix) {
			continue
		}

		// this is our infiniband device
		if strings.HasPrefix(devName, ourPrefix) {
			ourInfinibandDevs = append(ourInfinibandDevs, devName)
			continue
		}

		// this someone else's infiniband device
		tmp = append(tmp, devName)
	}

	residualInfinibandDevs := []string{}
	for _, peerDevName := range tmp {
		idx := strings.LastIndex(peerDevName, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid infiniband device name \"%s\"", peerDevName)
		}
		rPeerPath := peerDevName[idx+1:]
		rPeerPath = strings.Replace(rPeerPath, "-", "/", -1)
		absPeerPath := fmt.Sprintf("/%s", rPeerPath)
		residualInfinibandDevs = append(residualInfinibandDevs, absPeerPath)
	}

	ourName := fmt.Sprintf("infiniband.unix.%s", deviceName)
	for _, devName := range ourInfinibandDevs {
		idx := strings.LastIndex(devName, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid infiniband device name \"%s\"", devName)
		}
		rPath := devName[idx+1:]
		rPath = strings.Replace(rPath, "-", "/", -1)
		absPath := fmt.Sprintf("/%s", rPath)

		dummyDevice := config.Device{
			"path": absPath,
		}

		if len(residualInfinibandDevs) == 0 {
			err := c.removeUnixDevice(ourName, dummyDevice, true)
			if err != nil {
				return err
			}
			continue
		}

		eject := true
		for _, peerDevPath := range residualInfinibandDevs {
			if peerDevPath == absPath {
				eject = false
				break
			}
		}

		err := c.removeUnixDevice(ourName, dummyDevice, eject)
		if err != nil {
			return err
		}
	}

	// Remove the interface from the container.
	hostName := c.localConfig[fmt.Sprintf("volatile.%s.host_name", deviceName)]
	if hostName != "" {
		err = c.c.DetachInterfaceRename(device["name"], hostName)
		if err != nil {
			return errors.Wrapf(err, "Failed to detach interface: %s to %s", device["name"], hostName)
		}
	}

	err = c.clearInfinibandVolatile(deviceName)
	if err != nil {
		return err
	}

	return nil
}

func (c *containerLXC) clearInfinibandVolatile(deviceName string) error {
	configKey := fmt.Sprintf("volatile.%s.host_name", deviceName)
	err := c.VolatileSet(map[string]string{configKey: ""})
	if err != nil {
		return err
	}

	return nil
}
