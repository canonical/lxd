package device

import (
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
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

// Add returns nil error as majority of devices don't need to do any host-side setup.
func (d *deviceCommon) Add() error {
	return nil
}

// Register returns nil error as majority of devices don't need to do any event registration.
func (d *deviceCommon) Register() error {
	return nil
}

// CanHotPlug returns true as majority of devices can be started/stopped when instance is running.
// Also returns an empty list of update fields as most devices do not support live updates.
func (d *deviceCommon) CanHotPlug() (bool, []string) {
	return true, []string{}
}

// Update returns an error as most devices do not support live updates without being restarted.
func (d *deviceCommon) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	return fmt.Errorf("Device does not support updates whilst started")
}

// Remove returns nil error as majority of devices don't need to do any host-side cleanup on delete.
func (d *deviceCommon) Remove() error {
	return nil
}
