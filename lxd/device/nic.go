package device

import (
	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
)

// nicTypes defines the supported nic type devices and defines their creation functions.
var nicTypes = map[string]func() device{
	"physical": func() device { return &nicPhysical{} },
	"ipvlan":   func() device { return &nicIPVLAN{} },
	"p2p":      func() device { return &nicP2P{} },
	"bridged":  func() device { return &nicBridged{} },
	"macvlan":  func() device { return &nicMACVLAN{} },
	"sriov":    func() device { return &nicSRIOV{} },
}

// nicLoadByType returns a NIC device instantiated with supplied config.
func nicLoadByType(c config.Device) device {
	if f := nicTypes[c["nictype"]]; f != nil {
		return f()
	}
	return nil
}

// nicValidationRules returns config validation rules for nic devices.
func nicValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		"name":                    shared.IsAny,
		"parent":                  shared.IsAny,
		"mtu":                     shared.IsAny,
		"vlan":                    shared.IsAny,
		"hwaddr":                  shared.IsAny,
		"host_name":               shared.IsAny,
		"limits.ingress":          shared.IsAny,
		"limits.egress":           shared.IsAny,
		"limits.max":              shared.IsAny,
		"security.mac_filtering":  shared.IsAny,
		"security.ipv4_filtering": shared.IsAny,
		"security.ipv6_filtering": shared.IsAny,
		"maas.subnet.ipv4":        shared.IsAny,
		"maas.subnet.ipv6":        shared.IsAny,
		"ipv4.address":            NetworkValidAddressV4,
		"ipv6.address":            NetworkValidAddressV6,
		"ipv4.routes":             NetworkValidNetworkV4List,
		"ipv6.routes":             NetworkValidNetworkV6List,
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
