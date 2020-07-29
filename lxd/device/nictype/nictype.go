// Package nictype is a small package to allow resolving NIC "network" key to "nictype" key.
// It is it's own package to avoid circular dependency issues.
package nictype

import (
	"fmt"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
)

// NICType resolves the NIC Type for the supplied NIC device config.
// If the device "type" is "nic" and the "network" property is specified in the device config, then NIC type is
// resolved from the network's type. Otherwise the device's "nictype" property is returned (which may be empty if
// used with non-NIC device configs).
func NICType(s *state.State, d deviceConfig.Device) (string, error) {
	// NIC devices support resolving their "nictype" from their "network" property.
	if d["type"] == "nic" {
		if d["network"] != "" {
			_, netInfo, err := s.Cluster.GetNetworkInAnyState(d["network"])
			if err != nil {
				return "", errors.Wrapf(err, "Failed to load network %q", d["network"])
			}

			var nicType string
			switch netInfo.Type {
			case "bridge":
				nicType = "bridged"
			case "macvlan":
				nicType = "macvlan"
			case "sriov":
				nicType = "sriov"
			default:
				return "", fmt.Errorf("Unrecognised NIC network type for network %q", d["network"])
			}

			return nicType, nil
		}

	}

	// Infiniband devices use "nictype" without supporting "network" property, so just return it directly,
	// which is the same as accessing the property directly from the config.
	return d["nictype"], nil
}
