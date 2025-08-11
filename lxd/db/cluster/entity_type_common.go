package cluster

// entityTypeCommon acts as a base entityTypeDBInfo.
type entityTypeCommon struct{}

// allURLsQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) allURLsQuery() string {
	return ""
}

// urlsByProjectQuery returns empty because not all entityTypeDBInfo are project specific.
func (e entityTypeCommon) urlsByProjectQuery() string {
	return ""
}

// urlByIDQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) urlByIDQuery() string {
	return ""
}

// idFromURLQuery returns empty because not all entityTypeDBInfo implementations have one (see entityTypeServer).
func (e entityTypeCommon) idFromURLQuery() string {
	return ""
}

// onDeleteTriggerSQL returns empty because not all entityTypeDBInfo implementations have triggers (e.g. entityTypeServer, entityTypeCertificate).
func (e entityTypeCommon) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}
