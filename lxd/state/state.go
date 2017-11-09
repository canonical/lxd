package state

import (
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	DB *db.Node
	OS *sys.OS
}

// NewState returns a new State object with the given database and operating
// system components.
func NewState(db *db.Node, os *sys.OS) *State {
	return &State{
		DB: db,
		OS: os,
	}
}
