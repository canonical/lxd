//go:build linux && cgo && !agent

package state

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/bgp"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/dns"
	"github.com/canonical/lxd/lxd/endpoints"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/firewall"
	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/maas"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/lxd/ubuntupro"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/cancel"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	// Shutdown Context
	ShutdownCtx context.Context

	// Databases
	DB *db.DB

	// MAAS server
	MAAS *maas.Controller

	// BGP server
	BGP *bgp.Server

	// DNS server
	DNS *dns.Server

	// OS access
	OS    *sys.OS
	Proxy func(req *http.Request) (*url.URL, error)

	// LXD server
	Endpoints *endpoints.Endpoints

	// Event server
	DevlxdEvents *events.DevLXDServer
	Events       *events.Server

	// Firewall instance
	Firewall firewall.Firewall

	// Server certificate
	ServerCert func() *shared.CertInfo

	// Identity cache
	IdentityCache *identity.Cache

	// UpdateIdentityCache refreshes the local cache of identities.
	// This should be called whenever an identity is added, modified, or removed.
	// The cache is also refreshed on dqlite heartbeat to synchronise with other members.
	UpdateIdentityCache func()

	// Available instance types based on operational drivers.
	InstanceTypes map[instancetype.Type]error

	// Filesystem monitor
	DevMonitor fsmonitor.FSMonitor

	// Global configuration
	GlobalConfig *clusterConfig.Config

	// Local configuration
	LocalConfig *node.Config

	// Local server name.
	ServerName string

	// Whether the server is clustered.
	ServerClustered bool

	// Whether we are the leader and the leader address if not.
	LeaderInfo func() (*LeaderInfo, error)

	// Storage path used by this daemon
	ImagesStoragePath func(string) string

	// Storage path used by this daemon
	BackupsStoragePath func(string) string

	// Local server start time.
	StartTime time.Time

	// Authorizer.
	Authorizer auth.Authorizer

	// Ubuntu pro settings.
	UbuntuPro *ubuntupro.Client

	// NetworkReady can be used to track whether all networks are successfully started.
	NetworkReady cancel.Canceller

	// StorageReady can be used to track whether all storage pools are successfully started.
	StorageReady cancel.Canceller

	// CoreAuthSecrets returns the current secrets.
	CoreAuthSecrets func(ctx context.Context) (cluster.AuthSecrets, error)
}

// LeaderInfo represents information regarding cluster member leadership.
type LeaderInfo struct {
	// Clustered is true if the server is clustered and false otherwise.
	Clustered bool

	// Leader is true if the server is the raft leader or if the server is not clustered, and false otherwise.
	Leader bool

	// Address is the address of the leader. It is not set if the server is not clustered.
	Address string
}
