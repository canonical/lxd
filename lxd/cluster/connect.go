package cluster

import (
	"fmt"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
)

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for node-to-node communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// value 'lxd-cluster-notifier', which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, cert *shared.CertInfo, notify bool) (lxd.ContainerServer, error) {
	args := &lxd.ConnectionArgs{
		TLSServerCert: string(cert.PublicKey()),
		TLSClientCert: string(cert.PublicKey()),
		TLSClientKey:  string(cert.PrivateKey()),
		SkipGetServer: true,
	}
	if notify {
		args.UserAgent = "lxd-cluster-notifier"
	}

	url := fmt.Sprintf("https://%s", address)
	return lxd.ConnectLXD(url, args)
}
