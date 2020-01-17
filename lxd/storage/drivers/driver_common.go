package drivers

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/logger"
)

type common struct {
	name        string
	config      map[string]string
	getVolID    func(volType VolumeType, volName string) (int64, error)
	commonRules *Validators
	state       *state.State
	logger      logger.Logger
	patches     map[string]func() error
}

func (d *common) init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators) {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.commonRules = commonRules
	d.state = state
	d.logger = logger
}

func (d *common) load() error {
	return nil
}

// validatePool validates a pool config against common rules and optional driver specific rules.
func (d *common) validatePool(config map[string]string, driverRules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.PoolRules()

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return errors.Wrapf(err, "Invalid value for pool %q option %q", d.name, k)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		return fmt.Errorf("Invalid option for pool %q option %q", d.name, k)
	}

	return nil
}

// validateVolume validates a volume config against common rules and optional driver specific rules.
// This functions has a removeUnknownKeys option that if set to true will remove any unknown fields
// (excluding those starting with "user.") which can be used when translating a volume config to a
// different storage driver that has different options.
func (d *common) validateVolume(vol Volume, driverRules map[string]func(value string) error, removeUnknownKeys bool) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.VolumeRules(vol)

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(vol.config[k])
		if err != nil {
			return errors.Wrapf(err, "Invalid value for volume %q option %q", vol.name, k)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range vol.config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		if removeUnknownKeys {
			delete(vol.config, k)
		} else {
			return fmt.Errorf("Invalid option for volume %q option %q", vol.name, k)
		}
	}

	// If volume type is not custom, don't allow "size" property.
	if vol.volType != VolumeTypeCustom && vol.config["size"] != "" {
		return fmt.Errorf("Volume %q property is only valid for custom volume types", "size")
	}

	return nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *common) MigrationTypes(contentType ContentType, refresh bool) []migration.Type {
	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}

// Name returns the pool name.
func (d *common) Name() string {
	return d.name
}

// Logger returns the current logger.
func (d *common) Logger() logger.Logger {
	return d.logger
}

// Config returns the storage pool config (as a copy, so not modifiable).
func (d *common) Config() map[string]string {
	confCopy := make(map[string]string, len(d.config))
	for k, v := range d.config {
		confCopy[k] = v
	}

	return confCopy
}

// ApplyPatch looks for a suitable patch and runs it.
func (d *common) ApplyPatch(name string) error {
	if d.patches == nil {
		return fmt.Errorf("The patch mechanism isn't implemented on pool '%s'", d.name)
	}

	// Locate the patch.
	patch, ok := d.patches[name]
	if !ok {
		return fmt.Errorf("Patch '%s' isn't implemented on pool '%s'", name, d.name)
	}

	// Handle cases where a patch isn't needed.
	if patch == nil {
		return nil
	}

	return patch()
}
