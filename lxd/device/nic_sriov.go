package device

import (
	"fmt"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

type nicSRIOV struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicSRIOV) CanHotPlug() bool {
	return true
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicSRIOV) CanMigrate() bool {
	return d.config["network"] != ""
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
	if instConf.Type() == instancetype.Container || instConf.Type() == instancetype.Any {
		optionalFields = append(optionalFields, "mtu")
	}

	err := d.config.Validate(nicValidationRules(requiredFields, optionalFields, instConf))
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicSRIOV) validateEnvironment() error {
	if d.inst.Type() == instancetype.VM && shared.IsTrue(d.inst.ExpandedConfig()["migration.stateful"]) {
		return fmt.Errorf("Network SR-IOV devices cannot be used when migration.stateful is enabled")
	}

	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	if !network.InterfaceExists(d.config["parent"]) {
		return fmt.Errorf("Parent device %q doesn't exist", d.config["parent"])
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

	vfPCIDev, pciIOMMUGroup, err := networkSRIOVSetupVF(d.deviceCommon, d.config["parent"], vfDev, vfID, saveData)
	if err != nil {
		return nil, err
	}

	if d.inst.Type() == instancetype.Container {
		err := networkSRIOVSetupContainerVFNIC(saveData["host_name"], d.config)
		if err != nil {
			return nil, err
		}
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: saveData["host_name"]},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "pciSlotName", Value: vfPCIDev.SlotName},
				{Key: "pciIOMMUGroup", Value: fmt.Sprintf("%d", pciIOMMUGroup)},
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
		"last_state.vf.parent":     "",
		"last_state.vf.id":         "",
		"last_state.vf.hwaddr":     "",
		"last_state.vf.vlan":       "",
		"last_state.vf.spoofcheck": "",
		"last_state.pci.driver":    "",
	})

	v := d.volatileGet()

	err := networkSRIOVRestoreVF(d.deviceCommon, v)
	if err != nil {
		return err
	}

	return nil
}
