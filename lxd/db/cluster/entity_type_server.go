package cluster

import (
	"github.com/canonical/lxd/shared/entity"
)

// entityTypeServer implements entityType for a Server.
type entityTypeServer struct {
	entity.Server
}

// Code returns entityTypeCodeServer.
func (e entityTypeServer) Code() int64 {
	return entityTypeCodeServer
}

// AllURLsQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) AllURLsQuery() string {
	return ""
}

// URLsByProjectQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) URLsByProjectQuery() string {
	return ""
}

// URLByIDQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) URLByIDQuery() string {
	return ""
}

// IDFromURLQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) IDFromURLQuery() string {
	return ""
}

// OnDeleteTriggerName returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) OnDeleteTriggerName() string {
	return ""
}

// OnDeleteTriggerSQL  returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) OnDeleteTriggerSQL() string {
	return ""
}
