package drivers

import (
	"errors"
	"time"

	"github.com/grant-he/lxd/lxd/db"
	deviceConfig "github.com/grant-he/lxd/lxd/device/config"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/instance/instancetype"
	"github.com/grant-he/lxd/lxd/state"
	"github.com/grant-he/lxd/shared/api"
)

// common provides structure common to all instance types.
type common struct {
	dbType          instancetype.Type
	architecture    int
	devPaths        []string
	expandedConfig  map[string]string
	expandedDevices deviceConfig.Devices
	localConfig     map[string]string
	localDevices    deviceConfig.Devices
	profiles        []string
	project         string
	state           *state.State
}

// Project returns instance's project.
func (c *common) Project() string {
	return c.project
}

// Type returns the instance's type.
func (c *common) Type() instancetype.Type {
	return c.dbType
}

// Architecture returns the instance's architecture.
func (c *common) Architecture() int {
	return c.architecture
}

// ExpandedConfig returns instance's expanded config.
func (c *common) ExpandedConfig() map[string]string {
	return c.expandedConfig
}

// ExpandedDevices returns instance's expanded device config.
func (c *common) ExpandedDevices() deviceConfig.Devices {
	return c.expandedDevices
}

// LocalConfig returns the instance's local config.
func (c *common) LocalConfig() map[string]string {
	return c.localConfig
}

// LocalDevices returns the instance's local device config.
func (c *common) LocalDevices() deviceConfig.Devices {
	return c.localDevices
}

func (c *common) expandConfig(profiles []api.Profile) error {
	if profiles == nil && len(c.profiles) > 0 {
		var err error
		profiles, err = c.state.Cluster.GetProfiles(c.project, c.profiles)
		if err != nil {
			return err
		}
	}

	c.expandedConfig = db.ExpandInstanceConfig(c.localConfig, profiles)

	return nil
}

func (c *common) expandDevices(profiles []api.Profile) error {
	if profiles == nil && len(c.profiles) > 0 {
		var err error
		profiles, err = c.state.Cluster.GetProfiles(c.project, c.profiles)
		if err != nil {
			return err
		}
	}

	c.expandedDevices = db.ExpandInstanceDevices(c.localDevices, profiles)

	return nil
}

// DevPaths() returns a list of /dev devices which the instance requires access to.
// This is function is only safe to call from within the security
// packages as called during instance startup, the rest of the time this
// will likely return nil.
func (c *common) DevPaths() []string {
	return c.devPaths
}

// restart handles instance restarts.
func (c *common) restart(inst instance.Instance, timeout time.Duration) error {
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
