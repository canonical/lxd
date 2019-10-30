package drivers

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
)

type common struct {
	name           string
	config         map[string]string
	getVolID       func(volType VolumeType, volName string) (int64, error)
	getCommonRules func() map[string]func(string) error
	state          *state.State
}

func (d *common) init(state *state.State, name string, config map[string]string, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRulesFunc func() map[string]func(string) error) {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.getCommonRules = commonRulesFunc
	d.state = state
}

// validateVolume validates a volume config against common rules and optional driver specific rules.
// This functions has a removeUnknownKeys option that if set to true will remove any unknown fields
// (excluding those starting with "user.") which can be used when translating a volume config to a
// different storage driver that has different options.
func (d *common) validateVolume(volConfig map[string]string, driverRules map[string]func(value string) error, removeUnknownKeys bool) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.getCommonRules()

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(volConfig[k])
		if err != nil {
			return fmt.Errorf("Invalid value for volume option %s: %v", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range volConfig {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		if removeUnknownKeys {
			delete(volConfig, k)
		} else {
			return fmt.Errorf("Invalid volume option: %s", k)
		}
	}

	return nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *common) MigrationTypes(contentType ContentType) []migration.Type {
	if contentType != ContentTypeFS {
		return nil
	}

	return []migration.Type{
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: []string{"xattrs", "delete", "compress", "bidirectional"},
		},
	}
}
