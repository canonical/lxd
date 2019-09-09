package device

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/instance"
)

type nicSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicSRIOV) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"mtu",
		"hwaddr",
		"vlan",
		"security.mac_filtering",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
	}
	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicSRIOV) validateEnvironment() error {
	if d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicSRIOV) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	reservedDevices, err := instanceGetReservedDevices(d.state, d.config)
	if err != nil {
		return nil, err
	}

	vfDev, vfID, err := d.findFreeVirtualFunction(reservedDevices)
	if err != nil {
		return nil, err
	}

	err = d.setupSriovParent(vfDev, vfID, saveData)
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

	// Bring the interface up.
	_, err = shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "up")
	if err != nil {
		return nil, fmt.Errorf("Failed to bring up the interface: %v", err)
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}
	runConf.NetworkInterface = []RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicSRIOV) Stop() (*RunConfig, error) {
	v := d.volatileGet()
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []RunConfigItem{
			{Key: "link", Value: v["host_name"]},
		},
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicSRIOV) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name":                "",
		"last_state.hwaddr":        "",
		"last_state.mtu":           "",
		"last_state.created":       "",
		"last_state.vf.id":         "",
		"last_state.vf.hwaddr":     "",
		"last_state.vf.vlan":       "",
		"last_state.vf.spoofcheck": "",
	})

	v := d.volatileGet()

	err := d.restoreSriovParent(v)
	if err != nil {
		return err
	}

	return nil
}

// findFreeVirtualFunction looks on the specified parent device for an unused virtual function.
// Returns the name of the interface and virtual function index ID if found, error if not.
func (d *nicSRIOV) findFreeVirtualFunction(reservedDevices map[string]struct{}) (string, int, error) {
	sriovNumVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", d.config["parent"])
	sriovTotalVFs := fmt.Sprintf("/sys/class/net/%s/device/sriov_totalvfs", d.config["parent"])

	// Verify that this is indeed a SR-IOV enabled device.
	if !shared.PathExists(sriovTotalVFs) {
		return "", 0, fmt.Errorf("Parent device '%s' doesn't support SR-IOV", d.config["parent"])
	}

	// Get parent dev_port and dev_id values.
	pfDevPort, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_port", d.config["parent"]))
	if err != nil {
		return "", 0, err
	}

	pfDevID, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_id", d.config["parent"]))
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
	_, err = shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "up")
	if err != nil {
		return "", 0, err
	}

	// Check if any VFs are already enabled.
	nicName := ""
	vfID := 0
	for i := 0; i < sriovNum; i++ {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i)) {
			continue
		}

		// Check if VF is already in use.
		empty, err := shared.PathIsEmpty(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i))
		if err != nil {
			return "", 0, err
		}
		if empty {
			continue
		}

		vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i)
		nicName, err = d.getFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
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
			return "", 0, fmt.Errorf("All virtual functions of sriov device '%s' seem to be in use", d.config["parent"])
		}

		// Bump the number of VFs to the maximum.
		err := ioutil.WriteFile(sriovNumVFs, []byte(sriovTotalVfsStr), 0644)
		if err != nil {
			return "", 0, err
		}

		// Use next free VF index.
		for i := sriovNum + 1; i < sriovTotal; i++ {
			vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", d.config["parent"], i)
			nicName, err = d.getFreeVFInterface(reservedDevices, vfListPath, pfDevID, pfDevPort)
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

// getFreeVFInterface checks the contents of the VF directory to find a free VF interface name that
// belongs to the same device and port as the parent. Returns VF interface name or empty string if
// no free interface found.
func (d *nicSRIOV) getFreeVFInterface(reservedDevices map[string]struct{}, vfListPath string, pfDevID []byte, pfDevPort []byte) (string, error) {
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

// setupSriovParent configures a SR-IOV virtual function (VF) device on parent and stores original
// properties of the physical device into voltatile for restoration on detach.
func (d *nicSRIOV) setupSriovParent(vfDevice string, vfID int, volatile map[string]string) error {
	// Retrieve VF settings from parent device.
	vfInfo, err := d.networkGetVirtFuncInfo(d.config["parent"], vfID)
	if err != nil {
		return err
	}

	// Record properties of VF settings on the parent device.
	volatile["last_state.vf.hwaddr"] = vfInfo.mac
	volatile["last_state.vf.id"] = fmt.Sprintf("%d", vfID)
	volatile["last_state.vf.vlan"] = fmt.Sprintf("%d", vfInfo.vlan)
	volatile["last_state.vf.spoofcheck"] = fmt.Sprintf("%t", vfInfo.spoofcheck)

	// Record the host interface we represents the VF device which we will move into instance.
	volatile["host_name"] = vfDevice
	volatile["last_state.created"] = "false" // Indicates don't delete device at stop time.

	// Record properties of VF device.
	err = networkSnapshotPhysicalNic(volatile["host_name"], volatile)
	if err != nil {
		return err
	}

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCISlot, err := d.networkGetVFDevicePCISlot(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Get the path to the VF device's driver now, as once it is unbound we won't be able to
	// determine its driver path in order to rebind it.
	vfDriverPath, err := d.networkGetVFDeviceDriverPath(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = d.networkDeviceUnbind(vfPCISlot, vfDriverPath)
	if err != nil {
		return err
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	defer d.networkDeviceBind(vfPCISlot, vfDriverPath)

	// Setup VF VLAN if specified.
	if d.config["vlan"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "vlan", d.config["vlan"])
		if err != nil {
			return err
		}
	}

	// Setup VF MAC spoofing protection if specified.
	// The ordering of this section is very important, as Intel cards require a very specific
	// order of setup to allow LXD to set custom MACs when using spoof check mode.
	if shared.IsTrue(d.config["security.mac_filtering"]) {
		// If no MAC specified in config, use current VF interface MAC.
		mac := d.config["hwaddr"]
		if mac == "" {
			mac = volatile["last_state.hwaddr"]
		}

		// Set MAC on VF (this combined with spoof checking prevents any other MAC being used).
		_, err = shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", mac)
		if err != nil {
			return err
		}

		// Now that MAC is set on VF, we can enable spoof checking.
		_, err = shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", "on")
		if err != nil {
			return err
		}
	} else {
		// Reset VF to ensure no previous MAC restriction exists.
		_, err := shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", "00:00:00:00:00:00")
		if err != nil {
			return err
		}

		// Ensure spoof checking is disabled if not enabled in instance.
		_, err = shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", "off")
		if err != nil {
			return err
		}
	}

	// Bind VF device onto the host so that the settings will take effect.
	err = d.networkDeviceBind(vfPCISlot, vfDriverPath)
	if err != nil {
		return err
	}

	// Wait for VF driver to be reloaded, this will remove the VF interface temporarily, and
	// it will re-appear shortly after. Unfortunately the time between sending the bind event
	// to the nic and it actually appearing on the host is non-zero, so we need to watch and wait,
	// otherwise next steps of applying settings to interface will fail.
	err = d.networkDeviceBindWait(volatile["host_name"])
	if err != nil {
		return err
	}

	return nil
}

// virtFuncInfo holds information about SR-IOV virtual functions.
type virtFuncInfo struct {
	mac        string
	vlan       int
	spoofcheck bool
}

// networkGetVirtFuncInfo returns info about an SR-IOV virtual function from the ip tool.
func (d *nicSRIOV) networkGetVirtFuncInfo(devName string, vfID int) (vf virtFuncInfo, err error) {
	cmd := exec.Command("ip", "link", "show", devName)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}

	err = cmd.Start()
	if err != nil {
		return
	}
	defer stdout.Close()

	// Try and match: "vf 1 MAC 00:00:00:00:00:00, vlan 4095, spoof checking off"
	reVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, vlan (\d+), spoof checking (\w+)`, vfID))

	// IP link command doesn't show the vlan property if its set to 0, so we need to detect that.
	// Try and match: "vf 1 MAC 00:00:00:00:00:00, spoof checking off"
	reNoVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, spoof checking (\w+)`, vfID))
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		// First try and find VF and reads its properties with VLAN activated.
		res := reVlan.FindStringSubmatch(scanner.Text())
		if len(res) == 4 {
			vlan, err := strconv.Atoi(res[2])
			if err != nil {
				return vf, err
			}

			vf.mac = res[1]
			vf.vlan = vlan
			vf.spoofcheck = shared.IsTrue(res[3])
			return vf, err
		}

		// Next try and find VF and reads its properties with VLAN missing.
		res = reNoVlan.FindStringSubmatch(scanner.Text())
		if len(res) == 3 {
			vf.mac = res[1]
			vf.vlan = 0 // Missing VLAN ID means 0 when resetting later.
			vf.spoofcheck = shared.IsTrue(res[2])
			return vf, err
		}
	}

	err = scanner.Err()
	if err != nil {
		return
	}

	return vf, fmt.Errorf("no matching virtual function found")
}

// networkGetVFDevicePCISlot returns the PCI slot name for a network virtual function device.
func (d *nicSRIOV) networkGetVFDevicePCISlot(vfID string) (string, error) {
	file, err := os.Open(fmt.Sprintf("/sys/class/net/%s/device/virtfn%s/uevent", d.config["parent"], vfID))
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Looking for something like this "PCI_SLOT_NAME=0000:05:10.0"
		fields := strings.SplitN(scanner.Text(), "=", 2)
		if len(fields) == 2 && fields[0] == "PCI_SLOT_NAME" {
			return fields[1], nil
		}
	}

	err = scanner.Err()
	if err != nil {
		return "", err
	}

	return "", fmt.Errorf("PCI_SLOT_NAME not found")
}

// networkGetVFDeviceDriverPath returns the path to the network virtual function device driver in /sys.
func (d *nicSRIOV) networkGetVFDeviceDriverPath(vfID string) (string, error) {
	return filepath.EvalSymlinks(fmt.Sprintf("/sys/class/net/%s/device/virtfn%s/driver", d.config["parent"], vfID))
}

// networkDeviceUnbind unbinds a network device from the OS using its PCI Slot Name and driver path.
func (d *nicSRIOV) networkDeviceUnbind(pciSlotName string, driverPath string) error {
	return ioutil.WriteFile(fmt.Sprintf("%s/unbind", driverPath), []byte(pciSlotName), 0600)
}

// networkDeviceUnbind binds a network device to the OS using its PCI Slot Name and driver path.
func (d *nicSRIOV) networkDeviceBind(pciSlotName string, driverPath string) error {
	return ioutil.WriteFile(fmt.Sprintf("%s/bind", driverPath), []byte(pciSlotName), 0600)
}

// networkDeviceBindWait waits for network interface to appear after being binded.
func (d *nicSRIOV) networkDeviceBindWait(devName string) error {
	for i := 0; i < 10; i++ {
		if shared.PathExists(fmt.Sprintf("/sys/class/net/%s", devName)) {
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("Bind of interface \"%s\" took too long", devName)
}

// restoreSriovParent restores SR-IOV parent device settings when removed from an instance using the
// volatile data that was stored when the device was first added with setupSriovParent().
func (d *nicSRIOV) restoreSriovParent(volatile map[string]string) error {
	// Nothing to do if we don't know the original device name or the VF ID.
	if volatile["host_name"] == "" || volatile["last_state.vf.id"] == "" || d.config["parent"] == "" {
		return nil
	}

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCISlot, err := d.networkGetVFDevicePCISlot(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Get the path to the VF device's driver now, as once it is unbound we won't be able to
	// determine its driver path in order to rebind it.
	vfDriverPath, err := d.networkGetVFDeviceDriverPath(volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = d.networkDeviceUnbind(vfPCISlot, vfDriverPath)
	if err != nil {
		return err
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	defer d.networkDeviceBind(vfPCISlot, vfDriverPath)

	// Reset VF VLAN if specified
	if volatile["last_state.vf.vlan"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "vlan", volatile["last_state.vf.vlan"])
		if err != nil {
			return err
		}
	}

	// Reset VF MAC spoofing protection if recorded. Do this first before resetting the MAC
	// to avoid any issues with zero MACs refusing to be set whilst spoof check is on.
	if volatile["last_state.vf.spoofcheck"] != "" {
		mode := "off"
		if shared.IsTrue(volatile["last_state.vf.spoofcheck"]) {
			mode = "on"
		}

		_, err := shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", mode)
		if err != nil {
			return err
		}
	}

	// Reset VF MAC specified if specified.
	if volatile["last_state.vf.hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", volatile["last_state.vf.hwaddr"])
		if err != nil {
			return err
		}
	}

	// Bind VF device onto the host so that the settings will take effect.
	err = d.networkDeviceBind(vfPCISlot, vfDriverPath)
	if err != nil {
		return err
	}

	// Wait for VF driver to be reloaded, this will remove the VF interface from the instance
	// and it will re-appear on the host. Unfortunately the time between sending the bind event
	// to the nic and it actually appearing on the host is non-zero, so we need to watch and wait,
	// otherwise next step of restoring MAC and MTU settings in restorePhysicalNic will fail.
	err = d.networkDeviceBindWait(volatile["host_name"])
	if err != nil {
		return err
	}

	// Restore VF interface settings.
	err = networkRestorePhysicalNic(volatile["host_name"], volatile)
	if err != nil {
		return err
	}

	return nil
}
