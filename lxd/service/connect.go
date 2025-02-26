package service

import (
	"net/http"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
)

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for service-to-service communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// UserAgentNotifier value, which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request, notify bool) (lxd.InstanceServer, error) {
	return nil, nil
}
