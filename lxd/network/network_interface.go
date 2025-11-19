package network

import (
	"context"
	"net"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Type represents a LXD network driver type.
type Type interface {
	FillConfig(ctx context.Context, config map[string]string) error
	Info() Info
	ValidateName(name string) error
	Type() string
	DBType() db.NetworkType
}

// Network represents an instantiated LXD network.
type Network interface {
	Type

	// Load.
	init(state *state.State, id int64, projectName string, netInfo *api.Network, netNodes map[int64]db.NetworkNode)

	// Config.
	Validate(ctx context.Context, config map[string]string) error
	ID() int64
	Name() string
	Project() string
	Description() string
	Status() string
	LocalStatus() string
	Config() map[string]string
	Locations() []string
	IsUsed(ctx context.Context) (bool, error)
	IsManaged() bool
	DHCPv4Subnet() *net.IPNet
	DHCPv6Subnet() *net.IPNet
	DHCPv4Ranges() []shared.IPRange
	DHCPv6Ranges() []shared.IPRange

	// Actions.
	Create(ctx context.Context, clientType request.ClientType) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Evacuate(ctx context.Context) error
	Restore(ctx context.Context) error
	Rename(ctx context.Context, name string) error
	Update(ctx context.Context, newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error
	HandleHeartbeat(ctx context.Context, heartbeatData *cluster.APIHeartbeat) error
	Delete(ctx context.Context, clientType request.ClientType) error
	handleDependencyChange(ctx context.Context, netName string, netConfig map[string]string, changedKeys []string) error

	// Status.
	State(ctx context.Context) (*api.NetworkState, error)
	Leases(ctx context.Context, projectName string, clientType request.ClientType) ([]api.NetworkLease, error)

	// Address Forwards.
	ForwardCreate(ctx context.Context, forward api.NetworkForwardsPost, clientType request.ClientType) (net.IP, error)
	ForwardUpdate(ctx context.Context, listenAddress string, newForward api.NetworkForwardPut, clientType request.ClientType) error
	ForwardDelete(ctx context.Context, listenAddress string, clientType request.ClientType) error

	// Load Balancers.
	LoadBalancerCreate(ctx context.Context, loadBalancer api.NetworkLoadBalancersPost, clientType request.ClientType) (net.IP, error)
	LoadBalancerUpdate(ctx context.Context, listenAddress string, newLoadBalancer api.NetworkLoadBalancerPut, clientType request.ClientType) error
	LoadBalancerDelete(ctx context.Context, listenAddress string, clientType request.ClientType) error

	// Peerings.
	PeerCreate(ctx context.Context, forward api.NetworkPeersPost) error
	PeerUpdate(ctx context.Context, peerName string, newPeer api.NetworkPeerPut) error
	PeerDelete(ctx context.Context, peerName string) error
	PeerUsedBy(ctx context.Context, peerName string) ([]string, error)
}
