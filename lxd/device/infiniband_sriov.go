package device

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/shared"
)

type infinibandSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *infinibandSRIOV) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
	}
	err := config.ValidateDevice(nicValidationRules(requiredFields, optionalFields), d.config)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *infinibandSRIOV) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *infinibandSRIOV) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	devices, err := infinibandLoadDevices()
	if err != nil {
		return nil, err
	}

	reservedDevices, err := instanceGetReservedDevices(d.state, d.config)
	if err != nil {
		return nil, err
	}

	vfDev, err := d.findFreeVirtualFunction(reservedDevices)
	if err != nil {
		return nil, err
	}

	saveData["host_name"] = vfDev
	ifDev, ok := devices[saveData["host_name"]]
	if !ok {
		return nil, fmt.Errorf("Specified infiniband device \"%s\" not found", saveData["host_name"])
	}

	// Record hwaddr and mtu before potentially modifying them.
	err = networkSnapshotPhysicalNic(saveData["host_name"], saveData)
	if err != nil {
		return nil, err
	}

	// Set the MAC address.
	if d.config["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "address", d.config["hwaddr"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU.
	if d.config["mtu"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "mtu", d.config["mtu"])
		if err != nil {
			return nil, fmt.Errorf("Failed to set the MTU: %s", err)
		}
	}

	runConf := RunConfig{}

	// Configure runConf with infiniband setup instructions.
	err = infinibandAddDevices(d.state, d.instance.DevicesPath(), d.name, &ifDev, &runConf)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf.NetworkInterface = []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	return &runConf, nil
}

// findFreeVirtualFunction looks on the specified parent device for an unused virtual function.
// Returns the name of the interface if found, error if not.
func (d *infinibandSRIOV) findFreeVirtualFunction(reservedDevices map[string]struct{}) (string, error) {
	sriovNumVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", d.config["parent"])
	sriovTotalVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_totalvfs", d.config["parent"])

	// Verify that this is indeed a SR-IOV enabled device.
	if !shared.PathExists(sriovTotalVFs) {
		return "", fmt.Errorf("Parent device '%s' doesn't support SR-IOV", d.config["parent"])
	}

	// Get parent dev_port and dev_id values.
	pfDevPort, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_port", d.config["parent"]))
	if err != nil {
		return "", err
	}

	pfDevID, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_id", d.config["parent"]))
	if err != nil {
		return "", err
	}

	// Get number of currently enabled VFs.
	sriovNumVfsBuf, err := ioutil.ReadFile(sriovNumVFs)
	if err != nil {
		return "", err
	}
	sriovNumVfsStr := strings.TrimSpace(string(sriovNumVfsBuf))
	sriovNum, err := strconv.Atoi(sriovNumVfsStr)
	if err != nil {
		return "", err
	}

	// Check if any VFs are already enabled.
	nicName := ""
	for i := 0; i < sriovNum; i++ {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i)) {
			continue
		}

		// Check if VF is already in use.
		empty, err := shared.PathIsEmpty(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i))
		if err != nil {
			return "", err
		}
		if empty {
			continue
		}

		vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i)
		nicName, err = NetworkSRIOVGetFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
		if err != nil {
			return "", err
		}

		// Found a free VF.
		if nicName != "" {
			break
		}
	}

	if nicName == "" {
		return "", fmt.Errorf("All virtual functions on parent device are already in use")
	}

	return nicName, nil
}

// Stop is run when the device is removed from the instance.
func (d *infinibandSRIOV) Stop() (*RunConfig, error) {
	v := d.volatileGet()
	runConf := RunConfig{
		PostHooks:        []func() error{d.postStop},
		NetworkInterface: []RunConfigItem{{Key: "link", Value: v["host_name"]}},
	}

	err := unixDeviceRemove(d.instance.DevicesPath(), IBDevPrefix, d.name, "", &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *infinibandSRIOV) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name":         "",
		"last_state.hwaddr": "",
		"last_state.mtu":    "",
	})

	// Remove infiniband host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.instance.DevicesPath(), IBDevPrefix, d.name, "")
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
	}

	// Restore hwaddr and mtu.
	v := d.volatileGet()
	if v["host_name"] != "" {
		err := networkRestorePhysicalNic(v["host_name"], v)
		if err != nil {
			return err
		}
	}

	return nil
}
