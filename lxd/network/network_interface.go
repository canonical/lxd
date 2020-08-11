package network

import (
	"net"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Network represents a LXD network.
type Network interface {
	// Load.
	init(state *state.State, id int64, name string, netType string, description string, config map[string]string, status string)
	fillConfig(config map[string]string) error

	// Config.
	ValidateName(name string) error
	Validate(config map[string]string) error
	ID() int64
	Name() string
	Type() string
	Status() string
	Config() map[string]string
	IsUsed() (bool, error)
	DHCPv4Subnet() *net.IPNet
	DHCPv6Subnet() *net.IPNet
	DHCPv4Ranges() []shared.IPRange
	DHCPv6Ranges() []shared.IPRange

	// Actions.
	Create(clusterNotification bool) error
	Start() error
	Stop() error
	Rename(name string) error
	Update(newNetwork api.NetworkPut, targetNode string, clusterNotification bool) error
	HandleHeartbeat(heartbeatData *cluster.APIHeartbeat) error
	Delete(clusterNotification bool) error
}
