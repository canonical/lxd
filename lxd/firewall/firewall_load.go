package firewall

import (
	"github.com/lxc/lxd/lxd/firewall/drivers"
)

// New returns an appropriate firewall implementation.
func New() Firewall {
	// TODO: Issue #6223: add startup logic to choose xtables or nftables
	return drivers.XTables{}
}
