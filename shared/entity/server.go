package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeServer is an instantiated Server for convenience.
var TypeServer = Server{}

// TypeNameServer is the TypeName for Server entities.
const TypeNameServer TypeName = "server"

// Server is an implementation of Type for Server entities.
type Server struct{}

// RequiresProject returns false for entity type Server.
func (t Server) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameServer.
func (t Server) Name() TypeName {
	return TypeNameServer
}

// PathTemplate returns the path template for entity type Server.
func (t Server) PathTemplate() []string {
	return []string{}
}

// URL returns a URL for entity type Server.
func (t Server) URL() *api.URL {
	return urlMust(t, "", "")
}

// String implements fmt.Stringer for Server entities.
func (t Server) String() string {
	return string(t.Name())
}
