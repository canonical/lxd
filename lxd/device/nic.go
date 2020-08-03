package device

import (
	"github.com/lxc/lxd/shared/validate"
)

// nicValidationRules returns config validation rules for nic devices.
func nicValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		"name":                    validate.IsAny,
		"parent":                  validate.IsAny,
		"network":                 validate.IsAny,
		"mtu":                     validate.IsAny,
		"vlan":                    validate.IsNetworkVLAN,
		"hwaddr":                  validate.IsNetworkMAC,
		"host_name":               validate.IsAny,
		"limits.ingress":          validate.IsAny,
		"limits.egress":           validate.IsAny,
		"limits.max":              validate.IsAny,
		"security.mac_filtering":  validate.IsAny,
		"security.ipv4_filtering": validate.IsAny,
		"security.ipv6_filtering": validate.IsAny,
		"maas.subnet.ipv4":        validate.IsAny,
		"maas.subnet.ipv6":        validate.IsAny,
		"ipv4.address":            validate.Optional(validate.IsNetworkAddressV4),
		"ipv6.address":            validate.Optional(validate.IsNetworkAddressV6),
		"ipv4.routes":             validate.Optional(validate.IsNetworkV4List),
		"ipv6.routes":             validate.Optional(validate.IsNetworkV6List),
		"boot.priority":           validate.Optional(validate.IsUint32),
		"ipv4.gateway":            networkValidGateway,
		"ipv6.gateway":            networkValidGateway,
		"ipv4.host_address":       validate.Optional(validate.IsNetworkAddressV4),
		"ipv6.host_address":       validate.Optional(validate.IsNetworkAddressV6),
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// nicHasAutoGateway takes the value of the "ipv4.gateway" or "ipv6.gateway" config keys and returns whether they
// specify whether the gateway mode is automatic or not
func nicHasAutoGateway(value string) bool {
	if value == "" || value == "auto" {
		return true
	}

	return false
}
