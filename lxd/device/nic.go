package device

import (
	"github.com/lxc/lxd/shared"
)

// nicValidationRules returns config validation rules for nic devices.
func nicValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		"name":                    shared.IsAny,
		"parent":                  shared.IsAny,
		"network":                 shared.IsAny,
		"mtu":                     shared.IsAny,
		"vlan":                    networkValidVLAN,
		"hwaddr":                  networkValidMAC,
		"host_name":               shared.IsAny,
		"limits.ingress":          shared.IsAny,
		"limits.egress":           shared.IsAny,
		"limits.max":              shared.IsAny,
		"security.mac_filtering":  shared.IsAny,
		"security.ipv4_filtering": shared.IsAny,
		"security.ipv6_filtering": shared.IsAny,
		"maas.subnet.ipv4":        shared.IsAny,
		"maas.subnet.ipv6":        shared.IsAny,
		"ipv4.address":            shared.IsNetworkAddressV4,
		"ipv6.address":            shared.IsNetworkAddressV6,
		"ipv4.routes":             shared.IsNetworkV4List,
		"ipv6.routes":             shared.IsNetworkV6List,
		"boot.priority":           shared.IsUint32,
		"ipv4.gateway":            networkValidGateway,
		"ipv6.gateway":            networkValidGateway,
		"ipv4.host_address":       shared.IsNetworkAddressV4,
		"ipv6.host_address":       shared.IsNetworkAddressV6,
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
			err := shared.IsNotEmpty(value)
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
