package device

import (
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

// deviceCommon represents the common struct for all devices.
type deviceCommon struct {
	logger      logger.Logger
	inst        instance.Instance
	name        string
	config      deviceConfig.Device
	state       *state.State
	volatileGet func() map[string]string
	volatileSet func(map[string]string) error
}

// init stores the Instance, daemon state, device name and config into device.
// It also needs to be provided with volatile get and set functions for the device to allow
// persistent data to be accessed. This is implemented as part of deviceCommon so that the majority
// of devices don't need to implement it and can just embed deviceCommon.
func (d *deviceCommon) init(inst instance.Instance, state *state.State, name string, conf deviceConfig.Device, volatileGet VolatileGetter, volatileSet VolatileSetter) {
	logCtx := log.Ctx{"driver": conf["type"], "device": name}
	if inst != nil {
		logCtx["project"] = inst.Project()
		logCtx["instance"] = inst.Name()
	}

	d.logger = logging.AddContext(logger.Log, logCtx)
	d.inst = inst
	d.name = name
	d.config = conf
	d.state = state
	d.volatileGet = volatileGet
	d.volatileSet = volatileSet
}

// Name returns the name of the device.
func (d *deviceCommon) Name() string {
	return d.name
}

// Config returns the config for the device.
func (d *deviceCommon) Config() deviceConfig.Device {
	return d.config
}

// Add returns nil error as majority of devices don't need to do any host-side setup.
func (d *deviceCommon) Add() error {
	return nil
}

// Register returns nil error as majority of devices don't need to do any event registration.
func (d *deviceCommon) Register() error {
	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running,
// Returns true if instance type is container, as majority of devices can be started/stopped when
// instance is running. If instance type is VM then returns false as this is not currently supported.
func (d *deviceCommon) CanHotPlug() bool {
	if d.inst.Type() == instancetype.Container {
		return true
	}

	return false
}

// UpdatableFields returns an empty list of updatable fields as most devices do not support updates.
func (d *deviceCommon) UpdatableFields() []string {
	return []string{}
}

// Update returns an ErrCannotUpdate error as most devices do not support updates.
func (d *deviceCommon) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	return ErrCannotUpdate
}

// Remove returns nil error as majority of devices don't need to do any host-side cleanup on delete.
func (d *deviceCommon) Remove() error {
	return nil
}
