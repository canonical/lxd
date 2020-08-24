package drivers

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func init() {
	// Expose load to the instance package, to avoid circular imports.
	instance.Load = load

	// Expose validDevices to the instance package, to avoid circular imports.
	instance.ValidDevices = validDevices

	// Expose create to the instance package, to avoid circular imports.
	instance.Create = create
}

// load creates the underlying instance type struct and returns it as an Instance.
func load(s *state.State, args db.InstanceArgs, profiles []api.Profile) (instance.Instance, error) {
	var inst instance.Instance
	var err error

	if args.Type == instancetype.Container {
		inst, err = lxcLoad(s, args, profiles)
	} else if args.Type == instancetype.VM {
		inst, err = qemuLoad(s, args, profiles)
	} else {
		return nil, fmt.Errorf("Invalid instance type for instance %s", args.Name)
	}

	if err != nil {
		return nil, err
	}

	return inst, nil
}

// validDevices validate instance device configs.
func validDevices(state *state.State, cluster *db.Cluster, projectName string, instanceType instancetype.Type, devices deviceConfig.Devices, expanded bool) error {
	// Empty device list
	if devices == nil {
		return nil
	}

	instConf := &common{
		dbType:       instanceType,
		localDevices: devices.Clone(),
		project:      projectName,
	}

	// In non-expanded validation expensive checks should be avoided.
	if expanded {
		// The devices being validated are already expanded, so just use the same
		// devices clone as we used for the main devices config.
		instConf.expandedDevices = instConf.localDevices
	}

	// Check each device individually using the device package.
	// Use instConf.localDevices so that the cloned config is passed into the driver, so it cannot modify it.
	for name, config := range instConf.localDevices {
		err := device.Validate(instConf, state, name, config)
		if err != nil {
			return errors.Wrapf(err, "Device validation failed for %q", name)
		}

	}

	// Check we have a root disk if in expanded validation mode.
	if expanded {
		_, _, err := shared.GetRootDiskDevice(devices.CloneNative())
		if err != nil {
			return errors.Wrap(err, "Failed detecting root disk device")
		}
	}

	return nil
}

func create(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	if args.Type == instancetype.Container {
		return lxcCreate(s, args)
	} else if args.Type == instancetype.VM {
		return qemuCreate(s, args)
	}

	return nil, fmt.Errorf("Instance type invalid")
}
