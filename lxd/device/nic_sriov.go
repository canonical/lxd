package device

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/pci"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

type nicSRIOV struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *nicSRIOV) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{"parent"}
	optionalFields := []string{
		"name",
		"hwaddr",
		"vlan",
		"security.mac_filtering",
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
		"boot.priority",
	}

	// For VMs only NIC properties that can be specified on the parent's VF settings are controllable.
	if instConf.Type() == instancetype.Container {
		optionalFields = append(optionalFields, "mtu")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicSRIOV) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["parent"])) {
		return fmt.Errorf("Parent device '%s' doesn't exist", d.config["parent"])
	}

	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicSRIOV) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	saveData := make(map[string]string)

	// If VM, then try and load the vfio-pci module first.
	if d.inst.Type() == instancetype.VM {
		err = util.LoadModule("vfio-pci")
		if err != nil {
			return nil, errors.Wrapf(err, "Error loading %q module", "vfio-pci")
		}
	}

	vfDev, vfID, err := network.SRIOVFindFreeVirtualFunction(d.state, d.config["parent"])
	if err != nil {
		return nil, err
	}

	vfPCIDev, err := d.setupSriovParent(vfDev, vfID, saveData)
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.Container {
		// Set the MAC address.
		if d.config["hwaddr"] != "" {
			_, err := shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "address", d.config["hwaddr"])
			if err != nil {
				return nil, fmt.Errorf("Failed to set the MAC address: %s", err)
			}
		}

		// Set the MTU.
		if d.config["mtu"] != "" {
			err = network.InterfaceSetMTU(saveData["host_name"], d.config["mtu"])
			if err != nil {
				return nil, err
			}
		}

		// Bring the interface up.
		_, err = shared.RunCommand("ip", "link", "set", "dev", saveData["host_name"], "up")
		if err != nil {
			return nil, fmt.Errorf("Failed to bring up the interface: %v", err)
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: vfPCIDev.SlotName},
			}...)
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *nicSRIOV) Stop() (*deviceConfig.RunConfig, error) {
	v := d.volatileGet()
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
		NetworkInterface: []deviceConfig.RunConfigItem{
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
		"last_state.pci.driver":    "",
	})

	v := d.volatileGet()

	err := d.restoreSriovParent(v)
	if err != nil {
		return err
	}

	return nil
}

// setupSriovParent configures a SR-IOV virtual function (VF) device on parent and stores original properties of
// the physical device into voltatile for restoration on detach. Returns VF PCI device info.
func (d *nicSRIOV) setupSriovParent(vfDevice string, vfID int, volatile map[string]string) (pci.Device, error) {
	var vfPCIDev pci.Device

	// Retrieve VF settings from parent device.
	vfInfo, err := d.networkGetVirtFuncInfo(d.config["parent"], vfID)
	if err != nil {
		return vfPCIDev, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Record properties of VF settings on the parent device.
	volatile["last_state.vf.hwaddr"] = vfInfo.Address
	volatile["last_state.vf.id"] = fmt.Sprintf("%d", vfID)
	volatile["last_state.vf.vlan"] = fmt.Sprintf("%d", vfInfo.VLANs[0]["vlan"])
	volatile["last_state.vf.spoofcheck"] = fmt.Sprintf("%t", vfInfo.SpoofCheck)

	// Record the host interface we represents the VF device which we will move into instance.
	volatile["host_name"] = vfDevice
	volatile["last_state.created"] = "false" // Indicates don't delete device at stop time.

	// Record properties of VF device.
	err = networkSnapshotPhysicalNic(volatile["host_name"], volatile)
	if err != nil {
		return vfPCIDev, err
	}

	// Get VF device's PCI Slot Name so we can unbind and rebind it from the host.
	vfPCIDev, err = network.SRIOVGetVFDevicePCISlot(d.config["parent"], volatile["last_state.vf.id"])
	if err != nil {
		return vfPCIDev, err
	}

	// Unbind VF device from the host so that the settings will take effect when we rebind it.
	err = pci.DeviceUnbind(vfPCIDev)
	if err != nil {
		return vfPCIDev, err
	}

	revert.Add(func() { pci.DeviceProbe(vfPCIDev) })

	// Setup VF VLAN if specified.
	if d.config["vlan"] != "" {
		_, err := shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "vlan", d.config["vlan"])
		if err != nil {
			return vfPCIDev, err
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
		_, err = shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", mac)
		if err != nil {
			return vfPCIDev, err
		}

		// Now that MAC is set on VF, we can enable spoof checking.
		_, err = shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", "on")
		if err != nil {
			return vfPCIDev, err
		}
	} else {
		// Try to reset VF to ensure no previous MAC restriction exists, as some devices require this
		// before being able to set a new VF MAC or disable spoofchecking. However some devices don't
		// allow it so ignore failures.
		shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", "00:00:00:00:00:00")

		// Ensure spoof checking is disabled if not enabled in instance.
		_, err = shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", "off")
		if err != nil {
			return vfPCIDev, err
		}

		// Set MAC on VF if specified (this should be passed through into VM when it is bound to vfio-pci).
		if d.inst.Type() == instancetype.VM {
			// If no MAC specified in config, use current VF interface MAC.
			mac := d.config["hwaddr"]
			if mac == "" {
				mac = volatile["last_state.hwaddr"]
			}

			_, err = shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", mac)
			if err != nil {
				return vfPCIDev, err
			}
		}
	}

	if d.inst.Type() == instancetype.Container {
		// Bind VF device onto the host so that the settings will take effect.
		// This will remove the VF interface temporarily, and it will re-appear shortly after.
		err = pci.DeviceProbe(vfPCIDev)
		if err != nil {
			return vfPCIDev, err
		}

		// Wait for VF driver to be reloaded. Unfortunately the time between sending the bind event
		// to the nic and it actually appearing on the host is non-zero, so we need to watch and wait,
		// otherwise next steps of applying settings to interface will fail.
		err = network.InterfaceBindWait(volatile["host_name"])
		if err != nil {
			return vfPCIDev, err
		}
	} else if d.inst.Type() == instancetype.VM {
		// Register VF device with vfio-pci driver so it can be passed to VM.
		err = pci.DeviceDriverOverride(vfPCIDev, "vfio-pci")
		if err != nil {
			return vfPCIDev, err
		}

		// Record original driver used by VF device for restore.
		volatile["last_state.pci.driver"] = vfPCIDev.Driver
	}

	revert.Success()
	return vfPCIDev, nil
}

// VirtFuncInfo holds information about SR-IOV virtual functions.
type VirtFuncInfo struct {
	VF         int              `json:"vf"`
	Address    string           `json:"address"`
	MAC        string           `json:"mac"` // Deprecated
	VLANs      []map[string]int `json:"vlan_list"`
	SpoofCheck bool             `json:"spoofchk"`
}

// networkGetVirtFuncInfo returns info about an SR-IOV virtual function from the ip tool.
func (d *nicSRIOV) networkGetVirtFuncInfo(devName string, vfID int) (VirtFuncInfo, error) {
	vf := VirtFuncInfo{}
	vfNotFoundErr := fmt.Errorf("no matching virtual function found")

	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return vf, fmt.Errorf("ip command not found")
	}

	// Function to get VF info using regex matching, for older versions of ip tool. Less reliable.
	vfFindByRegex := func(devName string, vfID int) (VirtFuncInfo, error) {
		cmd := exec.Command(ipPath, "link", "show", devName)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return vf, err
		}
		defer stdout.Close()

		err = cmd.Start()
		if err != nil {
			return vf, err
		}
		defer cmd.Wait()

		// Try and match: "vf 1 MAC 00:00:00:00:00:00, vlan 4095, spoof checking off"
		reVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, vlan (\d+), spoof checking (\w+)`, vfID))

		// IP link command doesn't show the vlan property if its set to 0, so we need to detect that.
		// Try and match: "vf 1 MAC 00:00:00:00:00:00, spoof checking off"
		reNoVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, spoof checking (\w+)`, vfID))
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			// First try and find VF and read its properties with VLAN activated.
			res := reVlan.FindStringSubmatch(scanner.Text())
			if len(res) == 4 {
				vlan, err := strconv.Atoi(res[2])
				if err != nil {
					return vf, err
				}

				vf.Address = res[1]
				vf.VLANs = append(vf.VLANs, map[string]int{"vlan": vlan})
				vf.SpoofCheck = shared.IsTrue(res[3])

				return vf, err
			}

			// Next try and find VF and read its properties with VLAN missing.
			res = reNoVlan.FindStringSubmatch(scanner.Text())
			if len(res) == 3 {
				vf.Address = res[1]
				// Missing VLAN ID means 0 when resetting later.
				vf.VLANs = append(vf.VLANs, map[string]int{"vlan": 0})
				vf.SpoofCheck = shared.IsTrue(res[2])

				return vf, err
			}
		}

		err = scanner.Err()
		if err != nil {
			return vf, err
		}

		return vf, vfNotFoundErr
	}

	// First try using the JSON output format as is more reliable to parse.
	cmd := exec.Command(ipPath, "-j", "link", "show", devName)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return vf, err
	}
	defer stdout.Close()

	err = cmd.Start()
	if err != nil {
		return vf, err
	}
	defer cmd.Wait()

	// Temporary struct to decode ip output into.
	var ifInfo []struct {
		VFList []VirtFuncInfo `json:"vfinfo_list"`
	}

	// Decode JSON output.
	dec := json.NewDecoder(stdout)
	err = dec.Decode(&ifInfo)
	if err != nil && err != io.EOF {
		return vf, err
	}

	err = cmd.Wait()
	if err != nil {
		// If JSON command fails, fallback to using regex match mode for older versions of ip tool.
		// This does not support the newer VF "link/ether" output prefix.
		return vfFindByRegex(devName, vfID)
	}

	if len(ifInfo) == 0 {
		return vf, vfNotFoundErr
	}

	// Search VFs returned for match.
	found := false
	for _, vfInfo := range ifInfo[0].VFList {
		if vfInfo.VF == vfID {
			vf = vfInfo // Found a match.
			found = true
		}
	}

	if !found {
		return vf, vfNotFoundErr
	}

	// Always populate VLANs slice if not already populated. Missing VLAN ID means 0 when resetting later.
	if len(vf.VLANs) == 0 {
		vf.VLANs = append(vf.VLANs, map[string]int{"vlan": 0})
	}

	// Ensure empty VLAN entry is consistently populated.
	if _, found = vf.VLANs[0]["vlan"]; !found {
		vf.VLANs[0]["vlan"] = 0
	}

	// If ip tool has provided old mac field, copy into newer address field.
	if vf.MAC != "" && vf.Address == "" {
		vf.Address = vf.MAC
	}

	return vf, nil
}

// networkGetVFDevicePCISlot returns the PCI slot name for a network virtual function device.
func (d *nicSRIOV) networkGetVFDevicePCISlot(vfID string) (pci.Device, error) {
	ueventFile := fmt.Sprintf("/sys/class/net/%s/device/virtfn%s/uevent", d.config["parent"], vfID)
	pciDev, err := pci.ParseUeventFile(ueventFile)
	if err != nil {
		return pciDev, err
	}

	return pciDev, nil
}

// restoreSriovParent restores SR-IOV parent device settings when removed from an instance using the
// volatile data that was stored when the device was first added with setupSriovParent().
func (d *nicSRIOV) restoreSriovParent(volatile map[string]string) error {
	// Nothing to do if we don't know the original device name or the VF ID.
	if volatile["host_name"] == "" || volatile["last_state.vf.id"] == "" || d.config["parent"] == "" {
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Get VF device's PCI info so we can unbind and rebind it from the host.
	vfPCIDev, err := network.SRIOVGetVFDevicePCISlot(d.config["parent"], volatile["last_state.vf.id"])
	if err != nil {
		return err
	}

	// Unbind VF device from the host so that the restored settings will take effect when we rebind it.
	err = pci.DeviceUnbind(vfPCIDev)
	if err != nil {
		return err
	}

	if d.inst.Type() == instancetype.VM {
		// Before we bind the device back to the host, ensure we restore the original driver info as it
		// should be currently set to vfio-pci.
		err = pci.DeviceSetDriverOverride(vfPCIDev, volatile["last_state.pci.driver"])
		if err != nil {
			return err
		}
	}

	// However we return from this function, we must try to rebind the VF so its not orphaned.
	// The OS won't let an already bound device be bound again so is safe to call twice.
	revert.Add(func() { pci.DeviceProbe(vfPCIDev) })

	// Reset VF VLAN if specified
	if volatile["last_state.vf.vlan"] != "" {
		_, err := shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "vlan", volatile["last_state.vf.vlan"])
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

		_, err := shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "spoofchk", mode)
		if err != nil {
			return err
		}
	}

	// Reset VF MAC specified if specified.
	if volatile["last_state.vf.hwaddr"] != "" {
		_, err := shared.TryRunCommand("ip", "link", "set", "dev", d.config["parent"], "vf", volatile["last_state.vf.id"], "mac", volatile["last_state.vf.hwaddr"])
		if err != nil {
			return err
		}
	}

	// Bind VF device onto the host so that the settings will take effect.
	err = pci.DeviceProbe(vfPCIDev)
	if err != nil {
		return err
	}

	// Wait for VF driver to be reloaded, this will remove the VF interface from the instance
	// and it will re-appear on the host. Unfortunately the time between sending the bind event
	// to the nic and it actually appearing on the host is non-zero, so we need to watch and wait,
	// otherwise next step of restoring MAC and MTU settings in restorePhysicalNic will fail.
	err = network.InterfaceBindWait(volatile["host_name"])
	if err != nil {
		return err
	}

	// Restore VF interface settings.
	err = networkRestorePhysicalNic(volatile["host_name"], volatile)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}
