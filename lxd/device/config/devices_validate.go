package config

import (
	"fmt"

	"github.com/lxc/lxd/shared"
)

// ValidateDevice accepts a map of field/validation functions to run against supplied config.
func ValidateDevice(rules map[string]func(value string) error, config map[string]string) error {
	checkedFields := map[string]struct{}{}

	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for device option %s: %v", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// Skip type fields are these are validated by the presence of an implementation.
		if k == "type" {
			continue
		}

		if k == "nictype" && shared.StringInSlice(config["type"], []string{"nic", "infiniband"}) {
			continue
		}

		return fmt.Errorf("Invalid device option: %s", k)
	}

	return nil
}
