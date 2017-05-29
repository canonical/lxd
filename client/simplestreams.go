package lxd

import (
	"net/http"

	"github.com/lxc/lxd/shared/simplestreams"
)

// ProtocolSimpleStreams implements a SimpleStreams API client
type ProtocolSimpleStreams struct {
	ssClient *simplestreams.SimpleStreams

	http            *http.Client
	httpHost        string
	httpUserAgent   string
	httpCertificate string
}

// GetConnectionInfo returns the basic connection information used to interact with the server
func (r *ProtocolSimpleStreams) GetConnectionInfo() (*ConnectionInfo, error) {
	info := ConnectionInfo{}
	info.Addresses = []string{r.httpHost}
	info.Certificate = r.httpCertificate
	info.Protocol = "simplestreams"

	return &info, nil
}
