package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/lxd/client"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for node-to-node communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// to the UserAgentNotifier value, which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request, notify bool) (lxd.InstanceServer, error) {
	// Wait for a connection to the events API first for non-notify connections.
	if !notify {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(10)*time.Second)
		defer cancel()
		err := EventListenerWait(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("Missing event connection with target cluster member")
		}
	}

	args := &lxd.ConnectionArgs{
		TLSServerCert: string(networkCert.PublicKey()),
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		SkipGetServer: true,
		UserAgent:     version.UserAgent,
	}

	if notify {
		args.UserAgent = clusterRequest.UserAgentNotifier
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

	url := fmt.Sprintf("https://%s", address)
	return lxd.ConnectLXD(url, args)
}

// ConnectIfInstanceIsRemote figures out the address of the cluster member which is running the instance with the
// given name in the specified project. If it's not the local member will connect to it and return the connected
// client (configured with the specified project), otherwise it will just return nil.
func ConnectIfInstanceIsRemote(s *state.State, projectName string, instName string, r *http.Request, instanceType instancetype.Type) (lxd.InstanceServer, error) {
	// No need to connect if not clustered.
	if !s.ServerClustered {
		return nil, nil
	}

	var address string // Cluster member address.
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		address, err = tx.GetNodeAddressOfInstance(ctx, projectName, instName, instanceType)
		return err
	})
	if err != nil {
		return nil, err
	}

	if address == "" {
		return nil, nil // The instance is running on this local member, no need to connect.
	}

	client, err := Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return nil, err
	}

	client = client.UseProject(projectName)

	return client, nil
}

// SetupTrust is a convenience around InstanceServer.CreateCertificate that adds the given server certificate to
// the trusted pool of the cluster at the given address, using the given token. The certificate is added as
// type CertificateTypeServer to allow intra-member communication. If a certificate with the same fingerprint
// already exists with a different name or type, then no error is returned.
func SetupTrust(serverCert *shared.CertInfo, clusterPut api.ClusterPut) error {
	// Connect to the target cluster node.
	args := &lxd.ConnectionArgs{
		TLSServerCert: clusterPut.ClusterCertificate,
		UserAgent:     version.UserAgent,
	}

	target, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", clusterPut.ClusterAddress), args)
	if err != nil {
		return fmt.Errorf("Failed to connect to target cluster node %q: %w", clusterPut.ClusterAddress, err)
	}

	cert, err := shared.GenerateTrustCertificate(serverCert, clusterPut.ServerName)
	if err != nil {
		return fmt.Errorf("Failed generating trust certificate: %w", err)
	}

	post := api.CertificatesPost{
		Name:        cert.Name,
		Type:        cert.Type,
		Projects:    cert.Projects,
		Restricted:  cert.Restricted,
		Certificate: cert.Certificate,
		TrustToken:  clusterPut.ClusterToken,
	}

	err = target.CreateCertificate(post)
	if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf("Failed to add server cert to cluster: %w", err)
	}

	return nil
}

// UpdateTrust ensures that the supplied certificate is stored in the target trust store with the correct name
// and type to ensure correct cluster operation. Should be called after SetupTrust. If a certificate with the same
// fingerprint is already in the trust store, but is of the wrong type or name then the existing certificate is
// updated to the correct type and name. If the existing certificate is the correct type but the wrong name then an
// error is returned. And if the existing certificate is the correct type and name then nothing more is done.
func UpdateTrust(serverCert *shared.CertInfo, serverName string, targetAddress string, targetCert string) error {
	// Connect to the target cluster node.
	args := &lxd.ConnectionArgs{
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return fmt.Errorf("Failed to connect to target cluster node %q: %w", targetAddress, err)
	}

	cert, err := shared.GenerateTrustCertificate(serverCert, serverName)
	if err != nil {
		return fmt.Errorf("Failed generating trust certificate: %w", err)
	}

	existingCert, _, err := target.GetCertificate(cert.Fingerprint)
	if err != nil {
		return fmt.Errorf("Failed getting existing certificate: %w", err)
	}

	if existingCert.Name != serverName && existingCert.Type == api.CertificateTypeServer {
		// Don't alter an existing server certificate that has our fingerprint but not our name.
		// Something is wrong as this shouldn't happen.
		return fmt.Errorf("Existing server certificate with different name %q already in trust store", existingCert.Name)
	} else if existingCert.Name != serverName && existingCert.Type != api.CertificateTypeServer {
		// Ensure that if a client certificate already exists that matches our fingerprint, that it
		// has the correct name and type for cluster operation, to allow us to associate member
		// server names to certificate names.
		err = target.UpdateCertificate(cert.Fingerprint, cert.Writable(), "")
		if err != nil {
			return fmt.Errorf("Failed updating certificate name and type in trust store: %w", err)
		}
	}

	return nil
}

// HasConnectivity probes the member with the given address for connectivity.
func HasConnectivity(networkCert *shared.CertInfo, serverCert *shared.CertInfo, address string) bool {
	config, err := tlsClientConfig(networkCert, serverCert)
	if err != nil {
		return false
	}

	var conn net.Conn
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err = tls.DialWithDialer(dialer, "tcp", address, config)
	if err == nil {
		_ = conn.Close()
		return true
	}

	return false
}
