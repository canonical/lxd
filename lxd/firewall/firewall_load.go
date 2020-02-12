package firewall

import (
	"github.com/lxc/lxd/lxd/firewall/drivers"
)

const driverXtables = "xtables"
const driverNftables = "nftables"

// New returns an appropriate firewall implementation.
// Uses xtables if nftables isn't compatible or isn't in use already, otherwise uses nftables.
func New() Firewall {
	nftables := drivers.Nftables{}
	xtables := drivers.Xtables{}

	// If nftables is compatible and already in use, then we prefer to use the nftables driver irrespective of
	// whether xtables is in use or not.
	nftablesCompat, nftablesInUse := nftables.Compat()
	if nftablesCompat && nftablesInUse {
		return nftables
	} else if !nftablesCompat {
		// Note: If nftables isn't compatible, we fallback to xtables without considering whether xtables
		// is itself compatible. This continues the existing behaviour of allowing LXD to start with
		// potentially an incomplete firewall backend, so that only networks and instances using those
		// features will fail to start later.
		return xtables
	}

	// If xtables is compatible and already in use, then we prefer to stick with the xtables driver rather than
	// mix the use of firewall drivers on the system.
	xtablesCompat, xtablesInUse := xtables.Compat()
	if xtablesCompat && xtablesInUse {
		return xtables
	}

	// Otherwise prefer nftables as default.
	return nftables
}
