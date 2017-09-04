package state

import (
	"database/sql"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	NodeDB *sql.DB
	DB     *db.Node
	OS     *sys.OS
}

// NewState returns a new State object with the given database and operating
// system components.
func NewState(nodeDB *sql.DB, db *db.Node, os *sys.OS) *State {
	return &State{
		NodeDB: nodeDB,
		DB:     db,
		OS:     os,
	}
}
