package cluster

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/client"
	clusterRequest "github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// ErrCertificateExists indicates that a certificate already exists.
var ErrCertificateExists error = fmt.Errorf("Certificate already in trust store")

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

			return shared.ProxyFromEnvironment(req)
		}

		args.Proxy = proxy
	}

	url := fmt.Sprintf("https://%s", address)
	return lxd.ConnectLXD(url, args)
}

// ConnectIfInstanceIsRemote figures out the address of the node which is
// running the container with the given name. If it's not the local node will
// connect to it and return the connected client, otherwise it will just return
// nil.
func ConnectIfInstanceIsRemote(cluster *db.Cluster, projectName string, name string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request, instanceType instancetype.Type) (lxd.InstanceServer, error) {
	var address string // Node address
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		address, err = tx.GetNodeAddressOfInstance(projectName, name, db.InstanceTypeFilter(instanceType))
		return err
	})
	if err != nil {
		return nil, err
	}
	if address == "" {
		// The instance is running right on this node, no need to connect.
		return nil, nil
	}

	return Connect(address, networkCert, serverCert, r, false)
}

// ConnectIfVolumeIsRemote figures out the address of the cluster member on which the volume with the given name is
// defined. If it's not the local cluster member it will connect to it and return the connected client, otherwise
// it just returns nil. If there is more than one cluster member with a matching volume name, an error is returned.
func ConnectIfVolumeIsRemote(s *state.State, poolName string, projectName string, volumeName string, volumeType int, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request) (lxd.InstanceServer, error) {
	localNodeID := s.Cluster.GetNodeID()
	var err error
	var nodes []db.NodeInfo
	var poolID int64
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		poolID, err = tx.GetStoragePoolID(poolName)
		if err != nil {
			return err
		}

		nodes, err = tx.GetStorageVolumeNodes(poolID, projectName, volumeName, volumeType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil && err != db.ErrNoClusterMember {
		return nil, err
	}

	// If volume uses a remote storage driver and so has no explicit cluster member, then we need to check
	// whether it is exclusively attached to remote instance, and if so then we need to forward the request to
	// the node whereit is currently used. This avoids conflicting with another member when using it locally.
	if err == db.ErrNoClusterMember {
		// GetLocalStoragePoolVolume returns a volume with an empty Location field for remote drivers.
		_, vol, err := s.Cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
		if err != nil {
			return nil, err
		}

		remoteInstance, err := storagePools.VolumeUsedByExclusiveRemoteInstancesWithProfiles(s, poolName, projectName, vol)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed checking if volume %q is available", volumeName)
		}

		if remoteInstance != nil {
			var instNode db.NodeInfo
			err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
				instNode, err = tx.GetNodeByName(remoteInstance.Node)
				return err
			})
			if err != nil {
				return nil, fmt.Errorf("Failed getting cluster member info for %q: %w", remoteInstance.Node, err)
			}

			// Replace node list with instance's cluster member node (which might be local member).
			nodes = []db.NodeInfo{instNode}
		} else {
			// Volume isn't exclusively attached to an instance. Use local cluster member.
			return nil, nil
		}
	}

	nodeCount := len(nodes)
	if nodeCount > 1 {
		return nil, fmt.Errorf("More than one cluster member has a volume named %q. Please target a specific member", volumeName)
	} else if nodeCount < 1 {
		// Should never get here.
		return nil, fmt.Errorf("Volume %q has empty cluster member list", volumeName)
	}

	node := nodes[0]
	if node.ID == localNodeID {
		// Use local cluster member if volume belongs to this local node.
		return nil, nil
	}

	// Connect to remote cluster member.
	return Connect(node.Address, networkCert, serverCert, r, false)
}

// SetupTrust is a convenience around InstanceServer.CreateCertificate that adds the given server certificate to
// the trusted pool of the cluster at the given address, using the given password. The certificate is added as
// type CertificateTypeServer to allow intra-member communication. If a certificate with the same fingerprint
// already exists with a different name or type, then no error is returned.
func SetupTrust(serverCert *shared.CertInfo, serverName string, targetAddress string, targetCert string, targetPassword string) error {
	// Connect to the target cluster node.
	args := &lxd.ConnectionArgs{
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return errors.Wrapf(err, "Failed to connect to target cluster node %q", targetAddress)
	}

	cert, err := generateTrustCertificate(serverCert, serverName)
	if err != nil {
		return errors.Wrapf(err, "Failed generating trust certificate")
	}

	post := api.CertificatesPost{
		CertificatePut: cert.CertificatePut,
		Password:       targetPassword,
	}

	err = target.CreateCertificate(post)
	if err != nil && err.Error() != ErrCertificateExists.Error() {
		return errors.Wrap(err, "Failed to add server cert to cluster")
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
		return errors.Wrapf(err, "Failed to connect to target cluster node %q", targetAddress)
	}

	cert, err := generateTrustCertificate(serverCert, serverName)
	if err != nil {
		return errors.Wrapf(err, "Failed generating trust certificate")
	}

	existingCert, _, err := target.GetCertificate(cert.Fingerprint)
	if err != nil {
		return errors.Wrapf(err, "Failed getting existing certificate")
	}

	if existingCert.Name != serverName && existingCert.Type == api.CertificateTypeServer {
		// Don't alter an existing server certificate that has our fingerprint but not our name.
		// Something is wrong as this shouldn't happen.
		return fmt.Errorf("Existing server certificate with different name %q already in trust store", existingCert.Name)
	} else if existingCert.Name != serverName && existingCert.Type != api.CertificateTypeServer {
		// Ensure that if a client certificate already exists that matches our fingerprint, that it
		// has the correct name and type for cluster operation, to allow us to associate member
		// server names to certificate names.
		err = target.UpdateCertificate(cert.Fingerprint, cert.CertificatePut, "")
		if err != nil {
			return errors.Wrap(err, "Failed updating certificate name and type in trust store")
		}
	}

	return nil
}

// generateTrustCertificate converts the specified serverCert and serverName into an api.Certificate suitable for
// use as a trusted cluster server certificate.
func generateTrustCertificate(serverCert *shared.CertInfo, serverName string) (*api.Certificate, error) {
	block, _ := pem.Decode(serverCert.PublicKey())
	if block == nil {
		return nil, fmt.Errorf("Failed to decode certificate")
	}

	fingerprint, err := shared.CertFingerprintStr(string(serverCert.PublicKey()))
	if err != nil {
		return nil, errors.Wrap(err, "Failed to calculate fingerprint")
	}

	certificate := base64.StdEncoding.EncodeToString(block.Bytes)
	cert := api.Certificate{
		CertificatePut: api.CertificatePut{
			Certificate: certificate,
			Name:        serverName,
			Type:        api.CertificateTypeServer, // Server type for intra-member communication.
		},
		Fingerprint: fingerprint,
	}

	return &cert, nil
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
		conn.Close()
		return true
	}
	return false
}
