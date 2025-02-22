//go:build !linux || !cgo || agent

package state

import (
	"context"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/events"
)

// State here is just an empty shim to statisfy dependencies.
type State struct {
	Events       *events.Server
	ShutdownCtx  context.Context
	ServerName   string
	GlobalConfig *clusterConfig.Config
}
