package clusterlinks

import (
	"encoding/json"
	"net/http"
	"net/url"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/version"
)

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for cluster-to-cluster communication.
func Connect(address string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request) (lxd.InstanceServer, error) {
	args := &lxd.ConnectionArgs{
		TLSServerCert: string(networkCert.PublicKey()),
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		SkipGetServer: true,
		UserAgent:     version.UserAgent,
	}

	if r != nil {
		proxy := func(req *http.Request) (*url.URL, error) {
			ctx := r.Context()

			val, ok := ctx.Value(request.CtxUsername).(string)
			if ok {
				req.Header.Add(request.HeaderForwardedUsername, val)
			}

			val, ok = ctx.Value(request.CtxProtocol).(string)
			if ok {
				req.Header.Add(request.HeaderForwardedProtocol, val)
			}

			req.Header.Add(request.HeaderForwardedAddress, r.RemoteAddr)

			identityProviderGroupsAny := ctx.Value(request.CtxIdentityProviderGroups)
			if ok {
				identityProviderGroups, ok := identityProviderGroupsAny.([]string)
				if ok {
					b, err := json.Marshal(identityProviderGroups)
					if err == nil {
						req.Header.Add(request.HeaderForwardedIdentityProviderGroups, string(b))
					}
				}
			}

			return shared.ProxyFromEnvironment(req)
		}

		args.Proxy = proxy
	}

	url := "https://" + address
	return lxd.ConnectLXD(url, args)
}
