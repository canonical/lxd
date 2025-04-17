package cluster

// entityTypeServer implements entityTypeDBInfo for a Server.
type entityTypeServer struct {
	entityTypeCommon
}

func (e entityTypeServer) code() int64 {
	return entityTypeCodeServer
}
