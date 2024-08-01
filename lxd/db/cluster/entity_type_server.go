package cluster

// entityTypeServer implements entityTypeDBInfo for a Server.
type entityTypeServer struct{}

func (e entityTypeServer) code() int64 {
	return entityTypeCodeServer
}

// allURLsQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) allURLsQuery() string {
	return ""
}

func (e entityTypeServer) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeServer) urlByIDQuery() string {
	return ""
}

// idFromURLQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeServer) idFromURLQuery() string {
	return ""
}

func (e entityTypeServer) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}
