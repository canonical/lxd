package drivers

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// Instance driver definitions.
var instanceDrivers = map[string]func() instance.Instance{
	"lxc":  func() instance.Instance { return &lxc{} },
	"qemu": func() instance.Instance { return &qemu{} },
}

// Supported instance types cache variables.
var supportedInstanceTypesMu sync.Mutex
var supportedInstanceTypes map[instancetype.Type]instance.Info
var instanceTypesWarnings []db.Warning

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

func create(s *state.State, args db.InstanceArgs, volumeConfig map[string]string, revert *revert.Reverter) (instance.Instance, error) {
	if args.Type == instancetype.Container {
		return lxcCreate(s, args, volumeConfig, revert)
	} else if args.Type == instancetype.VM {
		return qemuCreate(s, args, volumeConfig, revert)
	}

	return nil, fmt.Errorf("Instance type invalid")
}

// SupportedInstanceTypes returns a map of Info structs for all operational instance type drivers.
// The first time this function is called each of the instance drivers will be probed for support and the result
// will be cached internally to make subsequent calls faster.
func SupportedInstanceTypes() (map[instancetype.Type]instance.Info, []db.Warning) {
	supportedInstanceTypesMu.Lock()
	defer supportedInstanceTypesMu.Unlock()

	if supportedInstanceTypes != nil {
		return supportedInstanceTypes, instanceTypesWarnings
	}

	supportedInstanceTypes = make(map[instancetype.Type]instance.Info, len(instanceDrivers))
	instanceTypesWarnings = []db.Warning{}

	for _, instanceDriver := range instanceDrivers {
		driverInfo := instanceDriver().Info()

		if driverInfo.Error != nil || driverInfo.Version == "" {
			logger.Warn("Instance type not operational", log.Ctx{"type": driverInfo.Type, "driver": driverInfo.Name, "err": driverInfo.Error})
			instanceTypesWarnings = append(instanceTypesWarnings, db.Warning{
				TypeCode:    db.WarningInstanceTypeNotOperational,
				LastMessage: fmt.Sprintf("%v", driverInfo.Error),
			})
			continue
		}

		supportedInstanceTypes[driverInfo.Type] = driverInfo
	}

	return supportedInstanceTypes, instanceTypesWarnings
}
