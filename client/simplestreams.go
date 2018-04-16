package lxd

import (
	"fmt"
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
	info.URL = r.httpHost

	return &info, nil
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolSimpleStreams) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, fmt.Errorf("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}
