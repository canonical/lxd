package network

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// sriovReservedDevicesMutex used to coordinate access for checking reserved devices.
var sriovReservedDevicesMutex sync.Mutex

// SRIOVGetHostDevicesInUse returns a map of host device names that have been used by devices in other instances
// on the local node. Used for selecting physical and SR-IOV VF devices.
func SRIOVGetHostDevicesInUse(s *state.State, m deviceConfig.Device) (map[string]struct{}, error) {
	sriovReservedDevicesMutex.Lock()
	defer sriovReservedDevicesMutex.Unlock()

	var err error
	var localNode string
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		localNode, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return nil, errors.Wrapf(err, "Fetch node name")
	}

	filter := db.InstanceFilter{
		Project: "", // All projects.
		Node:    localNode,
		Type:    instancetype.Any,
	}

	reservedDevices := map[string]struct{}{}
	err = s.Cluster.InstanceList(&filter, func(dbInst db.Instance, p api.Project, profiles []api.Profile) error {
		// Expand configs so we take into account profile devices.
		dbInst.Config = db.ExpandInstanceConfig(dbInst.Config, profiles)
		dbInst.Devices = db.ExpandInstanceDevices(deviceConfig.NewDevices(dbInst.Devices), profiles).CloneNative()

		for devName, devConfig := range dbInst.Devices {
			// Record all parent devices, these are not eligible for use as physical or SR-IOV parents
			// for selecting VF devices.
			parent := devConfig["parent"]
			reservedDevices[parent] = struct{}{}

			// If the device on another instance has the same device type as us, and has the same
			// parent as us, and a non-empty host_name, then mark that host_name as reserved, as that
			// device is using it as a SR-IOV VF.
			if devConfig["type"] == m["type"] && parent == m["parent"] {
				hostName := dbInst.Config[fmt.Sprintf("volatile.%s.host_name", devName)]
				if hostName != "" {
					reservedDevices[hostName] = struct{}{}
				}
			}
		}

		return nil
	})

	return reservedDevices, nil
}

// SRIOVFindFreeVirtualFunction looks on the specified parent device for an unused virtual function.
// Returns the name of the interface and virtual function index ID if found, error if not.
func SRIOVFindFreeVirtualFunction(s *state.State, m deviceConfig.Device) (string, int, error) {
	reservedDevices, err := SRIOVGetHostDevicesInUse(s, m)
	if err != nil {
		return "", 0, errors.Wrapf(err, "Failed getting in use device list")
	}

	parent := m["parent"]

	sriovNumVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", parent)
	sriovTotalVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_totalvfs", parent)

	// Verify that this is indeed a SR-IOV enabled device.
	if !shared.PathExists(sriovTotalVFs) {
		return "", 0, fmt.Errorf("Parent device %q doesn't support SR-IOV", parent)
	}

	// Get parent dev_port and dev_id values.
	pfDevPort, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_port", parent))
	if err != nil {
		return "", 0, err
	}

	pfDevID, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_id", parent))
	if err != nil {
		return "", 0, err
	}

	// Get number of currently enabled VFs.
	sriovNumVfsBuf, err := ioutil.ReadFile(sriovNumVFs)
	if err != nil {
		return "", 0, err
	}
	sriovNumVfsStr := strings.TrimSpace(string(sriovNumVfsBuf))
	sriovNum, err := strconv.Atoi(sriovNumVfsStr)
	if err != nil {
		return "", 0, err
	}

	// Get number of possible VFs.
	sriovTotalVfsBuf, err := ioutil.ReadFile(sriovTotalVFs)
	if err != nil {
		return "", 0, err
	}
	sriovTotalVfsStr := strings.TrimSpace(string(sriovTotalVfsBuf))
	sriovTotal, err := strconv.Atoi(sriovTotalVfsStr)
	if err != nil {
		return "", 0, err
	}

	// Ensure parent is up (needed for Intel at least).
	_, err = shared.RunCommand("ip", "link", "set", "dev", parent, "up")
	if err != nil {
		return "", 0, err
	}

	// Check if any VFs are already enabled.
	nicName := ""
	vfID := 0
	for i := 0; i < sriovNum; i++ {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parent, i)) {
			continue
		}

		// Check if VF is already in use.
		empty, err := shared.PathIsEmpty(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parent, i))
		if err != nil {
			return "", 0, err
		}
		if empty {
			continue
		}

		vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parent, i)
		nicName, err = sriovGetFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
		if err != nil {
			return "", 0, err
		}

		// Found a free VF.
		if nicName != "" {
			vfID = i
			break
		}
	}

	if nicName == "" {
		if sriovNum == sriovTotal {
			return "", 0, fmt.Errorf("All virtual functions of sriov device %q seem to be in use", parent)
		}

		// Bump the number of VFs to the maximum.
		err := ioutil.WriteFile(sriovNumVFs, []byte(sriovTotalVfsStr), 0644)
		if err != nil {
			return "", 0, err
		}

		// Use next free VF index.
		for i := sriovNum + 1; i < sriovTotal; i++ {
			vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parent, i)
			nicName, err = sriovGetFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
			if err != nil {
				return "", 0, err
			}

			// Found a free VF.
			if nicName != "" {
				vfID = i
				break
			}
		}
	}

	if nicName == "" {
		return "", 0, fmt.Errorf("All virtual functions on parent device are already in use")
	}

	return nicName, vfID, nil
}

// sriovGetFreeVFInterface checks the contents of the VF directory to find a free VF interface name that
// belongs to the same device and port as the parent. Returns VF interface name or empty string if
// no free interface found.
func sriovGetFreeVFInterface(reservedDevices map[string]struct{}, vfListPath string, pfDevID []byte, pfDevPort []byte) (string, error) {
	ents, err := ioutil.ReadDir(vfListPath)
	if err != nil {
		return "", err
	}

	for _, ent := range ents {
		// We can't use this VF interface as it is reserved by another device.
		_, exists := reservedDevices[ent.Name()]
		if exists {
			continue
		}

		// Get VF dev_port and dev_id values.
		vfDevPort, err := ioutil.ReadFile(fmt.Sprintf("%s/%s/dev_port", vfListPath, ent.Name()))
		if err != nil {
			return "", err
		}

		vfDevID, err := ioutil.ReadFile(fmt.Sprintf("%s/%s/dev_id", vfListPath, ent.Name()))
		if err != nil {
			return "", err
		}

		// Skip VFs if they do not relate to the same device and port as the parent PF.
		// Some card vendors change the device ID for each port.
		if bytes.Compare(pfDevPort, vfDevPort) != 0 || bytes.Compare(pfDevID, vfDevID) != 0 {
			continue
		}

		return ent.Name(), nil
	}

	return "", nil
}
