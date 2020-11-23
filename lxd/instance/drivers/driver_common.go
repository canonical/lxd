package drivers

import (
	"errors"
	"time"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// common provides structure common to all instance types.
type common struct {
	op    *operations.Operation
	state *state.State

	id              int
	dbType          instancetype.Type
	architecture    int
	devPaths        []string
	expandedConfig  map[string]string
	expandedDevices deviceConfig.Devices
	localConfig     map[string]string
	localDevices    deviceConfig.Devices
	profiles        []string
	project         string
	snapshot        bool
	creationDate    time.Time
	lastUsedDate    time.Time
	ephemeral       bool
	name            string
	description     string
	stateful        bool
	expiryDate      time.Time
	node            string
}

// Project returns instance's project.
func (d *common) Project() string {
	return d.project
}

// Type returns the instance's type.
func (d *common) Type() instancetype.Type {
	return d.dbType
}

// Architecture returns the instance's architecture.
func (d *common) Architecture() int {
	return d.architecture
}

// ExpandedConfig returns instance's expanded config.
func (d *common) ExpandedConfig() map[string]string {
	return d.expandedConfig
}

// ExpandedDevices returns instance's expanded device config.
func (d *common) ExpandedDevices() deviceConfig.Devices {
	return d.expandedDevices
}

// LocalConfig returns the instance's local config.
func (d *common) LocalConfig() map[string]string {
	return d.localConfig
}

// LocalDevices returns the instance's local device config.
func (d *common) LocalDevices() deviceConfig.Devices {
	return d.localDevices
}

func (d *common) expandConfig(profiles []api.Profile) error {
	if profiles == nil && len(d.profiles) > 0 {
		var err error
		profiles, err = d.state.Cluster.GetProfiles(d.project, d.profiles)
		if err != nil {
			return err
		}
	}

	d.expandedConfig = db.ExpandInstanceConfig(d.localConfig, profiles)

	return nil
}

func (d *common) expandDevices(profiles []api.Profile) error {
	if profiles == nil && len(d.profiles) > 0 {
		var err error
		profiles, err = d.state.Cluster.GetProfiles(d.project, d.profiles)
		if err != nil {
			return err
		}
	}

	d.expandedDevices = db.ExpandInstanceDevices(d.localDevices, profiles)

	return nil
}

// DevPaths() returns a list of /dev devices which the instance requires access to.
// This is function is only safe to call from within the security
// packages as called during instance startup, the rest of the time this
// will likely return nil.
func (d *common) DevPaths() []string {
	return d.devPaths
}

// restart handles instance restarts.
func (d *common) restart(inst instance.Instance, timeout time.Duration) error {
	if timeout == 0 {
		err := inst.Stop(false)
		if err != nil {
			return err
		}
	} else {
		if inst.IsFrozen() {
			return errors.New("Instance is not running")
		}

		err := inst.Shutdown(timeout * time.Second)
		if err != nil {
			return err
		}
	}

	return inst.Start(false)
}
