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
