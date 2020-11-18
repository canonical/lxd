// +build !linux !cgo agent

package state

import (
	"context"

	"github.com/grant-he/lxd/lxd/events"
)

// State here is just an empty shim to statisfy dependencies.
type State struct {
	Events  *events.Server
	Context context.Context
}
